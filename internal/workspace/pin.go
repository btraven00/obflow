package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/btraven00/obflow/internal/benchmark"
	"gopkg.in/yaml.v3"
)

// Per-module outcome statuses, shared by pin and track.
const (
	StatusPinned        = "pinned"         // branch/ref resolved to a SHA
	StatusAlreadyPinned = "already-pinned" // commit was already a SHA; left as-is
	StatusTracking      = "tracking"       // commit set to a branch name
	StatusNotFound      = "not-found"      // requested branch/ref absent in remote
	StatusUnchanged     = "unchanged"      // already at the requested value
	StatusError         = "error"
)

// PinResult records what happened for one module during Pin / PinRemote /
// TrackBranch. OldSHA/NewSHA hold whatever the commit field was/became — a SHA
// or a branch name depending on the operation.
type PinResult struct {
	Stage    string
	ModuleID string
	URL      string
	OldSHA   string
	NewSHA   string
	Status   string // one of the Status* constants
	Warning  string // human-readable detail (not-found reason, error text, ...)
	Err      error
}

// abbreviate shortens a full hex SHA to n chars, matching the plan convention
// of short commit ids. A non-SHA value (e.g. a branch name), n <= 0, or a value
// already shorter than n is returned unchanged.
func abbreviate(s string, n int) string {
	if n <= 0 || len(s) <= n || !looksLikeSHA(s) {
		return s
	}
	return s[:n]
}

// looksLikeSHA reports whether s is a hex git object name of at least 7 chars.
func looksLikeSHA(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// ResultsJSON renders results as a stable document for machine consumers (the
// CI bot): {"modules":[{module,url,old,new,status,detail}]}.
func ResultsJSON(rs []PinResult) ([]byte, error) {
	type row struct {
		Stage  string `json:"stage,omitempty"`
		Module string `json:"module"`
		URL    string `json:"url"`
		Old    string `json:"old"`
		New    string `json:"new"`
		Status string `json:"status"`
		Detail string `json:"detail,omitempty"`
	}
	rows := make([]row, 0, len(rs))
	for _, r := range rs {
		rows = append(rows, row{
			Stage:  r.Stage,
			Module: r.ModuleID,
			URL:    r.URL,
			Old:    r.OldSHA,
			New:    r.NewSHA,
			Status: r.Status,
			Detail: r.Warning,
		})
	}
	return json.MarshalIndent(struct {
		Modules []row `json:"modules"`
	}{rows}, "", "  ")
}

// Pin updates the canonical YAML's repository.commit for each module by
// reading `origin/<ref>` (after `git fetch origin`) in each module's
// clone. ref defaults to "HEAD" (i.e., origin/HEAD = upstream default
// branch) when empty.
//
// Mutates benchYAML in place. Returns per-module results so the caller
// can present diffs/warnings.
func Pin(benchYAML string, lock *Lock, ref string, abbrev int) ([]PinResult, error) {
	if ref == "" {
		ref = "HEAD"
	}
	benchDir, err := filepath.Abs(filepath.Dir(benchYAML))
	if err != nil {
		return nil, err
	}

	// Fetch + rev-parse for each module, in parallel.
	fetchResults := Fanout(benchDir, lock, func(dir string, _ LockedModule) (string, error) {
		if _, err := Git(dir, "fetch", "origin", "--quiet"); err != nil {
			return "", err
		}
		sha, err := Git(dir, "rev-parse", "origin/"+ref)
		if err != nil {
			return "", err
		}
		return sha, nil
	})

	urlToNew := map[string]string{}
	results := make([]PinResult, 0, len(lock.Modules))
	for _, fr := range fetchResults {
		pr := PinResult{Stage: fr.Module.Stage, ModuleID: fr.Module.ID, URL: fr.Module.Remote}
		if fr.Err != nil {
			pr.Err = fr.Err
			results = append(results, pr)
			continue
		}
		sha := abbreviate(fr.Out, abbrev)
		pr.NewSHA = sha
		urlToNew[normRemote(fr.Module.Remote)] = sha
		results = append(results, pr)
	}

	// Read YAML, capture old SHAs, rewrite, write back.
	root, err := readYAML(benchYAML)
	if err != nil {
		return results, err
	}
	urlToOld := map[string]string{}
	walkRepositories(root, func(repo *yaml.Node) {
		url := repoURL(repo)
		if url == "" {
			return
		}
		// capture old commit
		for i := 0; i+1 < len(repo.Content); i += 2 {
			k := repo.Content[i]
			v := repo.Content[i+1]
			if k.Value == "commit" && v.Kind == yaml.ScalarNode {
				urlToOld[normRemote(url)] = v.Value
			}
		}
		setMapStringValue(repo, "commit", func(old string) (string, bool) {
			n, ok := urlToNew[normRemote(url)]
			return n, ok
		})
	})

	for i := range results {
		key := normRemote(results[i].URL)
		results[i].OldSHA = urlToOld[key]
	}

	if _, err := writeYAML(root, benchYAML); err != nil {
		return results, err
	}

	// Ancestry checks (post-write; informational).
	for i := range results {
		if results[i].Err != nil || results[i].OldSHA == "" || results[i].NewSHA == "" {
			continue
		}
		mod := findModule(lock, results[i].ModuleID)
		if mod == nil {
			continue
		}
		dir := filepath.Join(benchDir, mod.Path)
		// Is OldSHA an ancestor of NewSHA? Non-fast-forward warning if not.
		_, err := Git(dir, "merge-base", "--is-ancestor", results[i].OldSHA, results[i].NewSHA)
		if err != nil && !strings.Contains(err.Error(), "exit status 1") {
			// Some other error; ignore for now.
			continue
		}
		if err != nil {
			results[i].Warning = fmt.Sprintf("not a fast-forward (old %s not ancestor of %s)", short(results[i].OldSHA), short(results[i].NewSHA))
		}
	}

	// Classify each result for structured/JSON output.
	for i := range results {
		switch {
		case results[i].Err != nil:
			results[i].Status = StatusError
		case results[i].OldSHA == results[i].NewSHA:
			results[i].Status = StatusUnchanged
		default:
			results[i].Status = StatusPinned
		}
	}

	return results, nil
}

// PinRemote freezes each module's tracked branch to a concrete SHA over the
// network with `git ls-remote` — no clones, no lock file (CI-friendly). The ref
// resolved per module is `ref` when non-empty (applied to every module, like
// `Pin --ref`), else the module's current commit value (dereference-in-place).
//
// A module whose commit is ALREADY a SHA is never re-pinned: it is left exactly
// as-is and reported StatusAlreadyPinned. This keeps `pin` idempotent and
// non-destructive — a deliberate freeze is never silently moved.
//
// Written SHAs are abbreviated to `abbrev` hex chars (0 = full).
//
// Mutates benchYAML in place. Returns per-module results so the caller can
// present a diff / structured feedback.
func PinRemote(benchYAML, ref string, abbrev int) ([]PinResult, error) {
	bench, err := benchmark.Load(benchYAML)
	if err != nil {
		return nil, err
	}

	// Resolve each unique (url, ref) once.
	type resolved struct {
		sha string
		err error
	}
	cache := map[string]resolved{}

	var results []PinResult
	for _, st := range bench.Stages {
		for _, mod := range st.Modules {
			url := mod.Repository.URL
			cur := mod.Repository.Commit
			pr := PinResult{Stage: st.ID, ModuleID: mod.ID, URL: url, OldSHA: cur, NewSHA: cur}

			// Never re-pin an existing pin.
			if looksLikeSHA(cur) {
				pr.Status = StatusAlreadyPinned
				results = append(results, pr)
				continue
			}

			useRef := ref
			if useRef == "" {
				useRef = cur
			}
			key := normRemote(url) + "\x00" + useRef
			r, ok := cache[key]
			if !ok {
				sha, e := resolveRemoteRef(url, useRef)
				r = resolved{sha: sha, err: e}
				cache[key] = r
			}
			switch {
			case r.err != nil:
				pr.Status = StatusError
				pr.Warning = r.err.Error()
				pr.Err = r.err
			case r.sha == "":
				pr.Status = StatusNotFound
				pr.Warning = fmt.Sprintf("ref %q not found in remote", useRef)
			default:
				pr.Status = StatusPinned
				pr.NewSHA = abbreviate(r.sha, abbrev)
			}
			results = append(results, pr)
		}
	}

	if err := applyResults(benchYAML, results); err != nil {
		return results, err
	}
	return results, nil
}

// TrackBranch is the inverse of PinRemote: it points each module's commit at the
// branch `branch`, but ONLY for modules whose remote actually has that branch.
// Modules lacking the branch are left untouched (StatusNotFound); modules
// already on it are StatusUnchanged. Use it to move modules from a frozen SHA
// (or another branch) back onto a live branch.
//
// Mutates benchYAML in place.
func TrackBranch(benchYAML, branch string) ([]PinResult, error) {
	if branch == "" {
		return nil, fmt.Errorf("track: a branch name is required")
	}
	bench, err := benchmark.Load(benchYAML)
	if err != nil {
		return nil, err
	}

	// Probe each unique url once.
	type probe struct {
		exists bool
		err    error
	}
	cache := map[string]probe{}

	var results []PinResult
	for _, st := range bench.Stages {
		for _, mod := range st.Modules {
			url := mod.Repository.URL
			cur := mod.Repository.Commit
			pr := PinResult{Stage: st.ID, ModuleID: mod.ID, URL: url, OldSHA: cur, NewSHA: cur}

			key := normRemote(url)
			p, ok := cache[key]
			if !ok {
				sha, e := remoteBranchSHA(url, branch)
				p = probe{exists: sha != "", err: e}
				cache[key] = p
			}
			switch {
			case p.err != nil:
				pr.Status = StatusError
				pr.Warning = p.err.Error()
				pr.Err = p.err
			case !p.exists:
				pr.Status = StatusNotFound
				pr.Warning = fmt.Sprintf("branch %q not found in remote", branch)
			case cur == branch:
				pr.Status = StatusUnchanged
			default:
				pr.Status = StatusTracking
				pr.NewSHA = branch
			}
			results = append(results, pr)
		}
	}

	if err := applyResults(benchYAML, results); err != nil {
		return results, err
	}
	return results, nil
}

var commitLineRe = regexp.MustCompile(`^(\s*commit:\s+)(\S+)(.*)$`)

// applyResults rewrites benchYAML's commit lines from the per-module results.
// Each result is paired POSITIONALLY with its repository's commit scalar (both
// are in document order), so modules sharing a repo URL but holding different
// commits are handled independently — only results whose New differs from Old
// are written. The edit is surgical (one line each): indentation, blank lines,
// and comments are preserved byte-for-byte, unlike a yaml.v3 round-trip which
// reflows the whole document.
func applyResults(benchYAML string, results []PinResult) error {
	src, err := os.ReadFile(benchYAML)
	if err != nil {
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return fmt.Errorf("parse %s: %w", benchYAML, err)
	}

	var commitNodes []*yaml.Node
	walkRepositories(&root, func(repo *yaml.Node) {
		commitNodes = append(commitNodes, repoCommitNode(repo))
	})
	if len(commitNodes) != len(results) {
		return fmt.Errorf("plan structure changed under us: %d repositories vs %d modules",
			len(commitNodes), len(results))
	}

	lineToNew := map[int]string{}
	for i, r := range results {
		if commitNodes[i] == nil || r.NewSHA == r.OldSHA {
			continue
		}
		lineToNew[commitNodes[i].Line] = quoteIfNumeric(r.NewSHA)
	}
	if len(lineToNew) == 0 {
		return nil
	}
	return os.WriteFile(benchYAML, editCommitLines(src, lineToNew), 0644)
}

// repoCommitNode returns the scalar value node of a repository's `commit` key,
// or nil if absent.
func repoCommitNode(repo *yaml.Node) *yaml.Node {
	for i := 0; i+1 < len(repo.Content); i += 2 {
		if repo.Content[i].Value == "commit" && repo.Content[i+1].Kind == yaml.ScalarNode {
			return repo.Content[i+1]
		}
	}
	return nil
}

// editCommitLines replaces the value token on each 1-based line in lineToNew,
// keeping that line's indentation, `commit:` key, and trailing inline comment.
func editCommitLines(src []byte, lineToNew map[int]string) []byte {
	lines := strings.Split(string(src), "\n")
	for i := range lines {
		v, ok := lineToNew[i+1]
		if !ok {
			continue
		}
		if m := commitLineRe.FindStringSubmatch(lines[i]); m != nil {
			lines[i] = m[1] + v + m[3]
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// quoteIfNumeric wraps an all-digit SHA in quotes so YAML keeps it a string.
// Real git SHAs almost always contain hex letters, but this is cheap insurance.
func quoteIfNumeric(s string) string {
	allDigits := len(s) > 0
	for _, r := range s {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return `"` + s + `"`
	}
	return s
}

// resolveRemoteRef returns the SHA that ref points to in the remote at url,
// using `git ls-remote` (no clone). Returns ("", nil) when ref does not resolve
// to any remote ref (e.g. it is already a SHA, or an unknown branch/tag).
func resolveRemoteRef(url, ref string) (string, error) {
	if url == "" || ref == "" {
		return "", nil
	}
	out, err := Git("", "ls-remote", url, ref)
	if err != nil {
		return "", err
	}
	return pickRefSHA(out, ref), nil
}

// remoteBranchSHA returns the SHA of refs/heads/<branch> in the remote at url,
// or "" if that branch does not exist there.
func remoteBranchSHA(url, branch string) (string, error) {
	if url == "" || branch == "" {
		return "", nil
	}
	out, err := Git("", "ls-remote", "--heads", url, branch)
	if err != nil {
		return "", err
	}
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 && f[1] == "refs/heads/"+branch {
			return f[0], nil
		}
	}
	return "", nil
}

// pickRefSHA selects a SHA from `git ls-remote` output (lines of "<sha>\t<ref>")
// for the requested ref, preferring an exact branch head, then a tag, then the
// first line. Returns "" when there are no usable matches.
func pickRefSHA(lsRemoteOut, ref string) string {
	out := strings.TrimSpace(lsRemoteOut)
	if out == "" {
		return ""
	}
	type match struct{ sha, name string }
	var matches []match
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 {
			matches = append(matches, match{sha: f[0], name: f[1]})
		}
	}
	if len(matches) == 0 {
		return ""
	}
	for _, m := range matches {
		if m.name == "refs/heads/"+ref {
			return m.sha
		}
	}
	for _, m := range matches {
		if m.name == "refs/tags/"+ref {
			return m.sha
		}
	}
	return matches[0].sha
}

func findModule(lock *Lock, id string) *LockedModule {
	for i := range lock.Modules {
		if lock.Modules[i].ID == id {
			return &lock.Modules[i]
		}
	}
	return nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
