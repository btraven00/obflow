package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/btraven00/obflow/internal/benchmark"
	"gopkg.in/yaml.v3"
)

// PinResult records what happened for one module during Pin.
type PinResult struct {
	ModuleID string
	URL      string
	OldSHA   string
	NewSHA   string
	Warning  string // non-fatal issue (e.g., not an ancestor)
	Err      error
}

// Pin updates the canonical YAML's repository.commit for each module by
// reading `origin/<ref>` (after `git fetch origin`) in each module's
// clone. ref defaults to "HEAD" (i.e., origin/HEAD = upstream default
// branch) when empty.
//
// Mutates benchYAML in place. Returns per-module results so the caller
// can present diffs/warnings.
func Pin(benchYAML string, lock *Lock, ref string) ([]PinResult, error) {
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
		pr := PinResult{ModuleID: fr.Module.ID, URL: fr.Module.Remote}
		if fr.Err != nil {
			pr.Err = fr.Err
			results = append(results, pr)
			continue
		}
		pr.NewSHA = fr.Out
		urlToNew[normRemote(fr.Module.Remote)] = fr.Out
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

	return results, nil
}

// PinRemote updates the canonical YAML's repository.commit for each module by
// resolving a ref to a SHA over the network with `git ls-remote` — no clones,
// no lock file (CI-friendly). The ref is `ref` when non-empty (applied to every
// module, matching `Pin --ref` semantics), else each module's current commit
// value (dereference-in-place). Refs that don't resolve (e.g. the field is
// already a SHA, or the branch is gone) are left unchanged with a Warning.
//
// Mutates benchYAML in place. Returns per-module results so the caller can
// present a diff.
func PinRemote(benchYAML, ref string) ([]PinResult, error) {
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
	urlToNew := map[string]string{}

	mods := bench.Modules()
	results := make([]PinResult, 0, len(mods))
	for _, mod := range mods {
		url := mod.Repository.URL
		cur := mod.Repository.Commit
		useRef := ref
		if useRef == "" {
			useRef = cur
		}
		pr := PinResult{ModuleID: mod.ID, URL: url, OldSHA: cur}

		key := normRemote(url) + "\x00" + useRef
		r, ok := cache[key]
		if !ok {
			sha, e := resolveRemoteRef(url, useRef)
			r = resolved{sha: sha, err: e}
			cache[key] = r
		}
		switch {
		case r.err != nil:
			pr.Err = r.err
		case r.sha == "":
			pr.NewSHA = cur
			pr.Warning = fmt.Sprintf("could not resolve %q (already a SHA or unknown ref); left unchanged", useRef)
		default:
			pr.NewSHA = r.sha
			urlToNew[normRemote(url)] = r.sha
		}
		results = append(results, pr)
	}

	// Surgically rewrite only the commit lines so the rest of the file —
	// indentation, blank lines, comments — is preserved byte-for-byte. A full
	// yaml.v3 round-trip (writeYAML) reflows the whole document, which would
	// bury the SHA changes in a huge diff when this runs as a bot PR.
	src, err := os.ReadFile(benchYAML)
	if err != nil {
		return results, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return results, fmt.Errorf("parse %s: %w", benchYAML, err)
	}
	out := rewriteCommitLines(src, &root, urlToNew)
	if err := os.WriteFile(benchYAML, out, 0644); err != nil {
		return results, err
	}
	return results, nil
}

var commitLineRe = regexp.MustCompile(`^(\s*commit:\s+)(\S+)(.*)$`)

// rewriteCommitLines returns src with each module's repository.commit replaced
// by urlToNew[url]. It locates the exact source line of every commit scalar via
// the parsed node tree (node.Line) and edits only that line, leaving leading
// indentation and any trailing inline comment untouched.
func rewriteCommitLines(src []byte, root *yaml.Node, urlToNew map[string]string) []byte {
	lineToNew := map[int]string{}
	walkRepositories(root, func(repo *yaml.Node) {
		url := repoURL(repo)
		if url == "" {
			return
		}
		newSHA, ok := urlToNew[normRemote(url)]
		if !ok {
			return
		}
		for i := 0; i+1 < len(repo.Content); i += 2 {
			k := repo.Content[i]
			v := repo.Content[i+1]
			if k.Value == "commit" && v.Kind == yaml.ScalarNode {
				if v.Value != newSHA {
					lineToNew[v.Line] = quoteIfNumeric(newSHA)
				}
				break
			}
		}
	})
	if len(lineToNew) == 0 {
		return src
	}
	lines := strings.Split(string(src), "\n")
	for i := range lines {
		newSHA, ok := lineToNew[i+1] // yaml node lines are 1-based
		if !ok {
			continue
		}
		if m := commitLineRe.FindStringSubmatch(lines[i]); m != nil {
			lines[i] = m[1] + newSHA + m[3]
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
