package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPickRefSHA(t *testing.T) {
	const sha = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	const tagSHA = "0000000000000000000000000000000000000000"

	cases := []struct {
		name string
		out  string
		ref  string
		want string
	}{
		{"empty (already a SHA)", "", "deadbee", ""},
		{"single branch", sha + "\trefs/heads/feature", "feature", sha},
		{
			"branch preferred over tag",
			tagSHA + "\trefs/tags/feature\n" + sha + "\trefs/heads/feature",
			"feature",
			sha,
		},
		{"tag when no branch", tagSHA + "\trefs/tags/v1", "v1", tagSHA},
		{"HEAD falls through to first", sha + "\tHEAD", "HEAD", sha},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickRefSHA(c.out, c.ref); got != c.want {
				t.Fatalf("pickRefSHA(%q, %q) = %q, want %q", c.out, c.ref, got, c.want)
			}
		})
	}
}

func TestLooksLikeSHA(t *testing.T) {
	// Drives the "never re-pin an existing pin" guard in PinRemote.
	sha := []string{
		"721958ba9e0a70301a9f3df6d6a81ecc3162a4e2", // full SHA
		"7cd9861", // abbreviated
		"abc1234",
	}
	notSHA := []string{
		"harmonize-argparsing", // branch
		"graphs",               // short branch
		"main",
		"v1.2.0", // tag-ish
		"",
		"feature/x",
		"abc123", // 6 chars — too short to be confident
	}
	for _, s := range sha {
		if !looksLikeSHA(s) {
			t.Errorf("looksLikeSHA(%q) = false, want true (would re-pin a frozen module)", s)
		}
	}
	for _, s := range notSHA {
		if looksLikeSHA(s) {
			t.Errorf("looksLikeSHA(%q) = true, want false (would skip a branch as if pinned)", s)
		}
	}
}

func TestAbbreviate(t *testing.T) {
	const full = "721958ba9e0a70301a9f3df6d6a81ecc3162a4e2"
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{full, 7, "721958b"},      // truncate to plan convention
		{full, 0, full},           // 0 = keep full
		{full, 40, full},          // already that length
		{"721958b", 7, "721958b"}, // already short
		{"harmonize-argparsing", 7, "harmonize-argparsing"}, // never truncate a branch name
		{"main", 7, "main"},
	}
	for _, c := range cases {
		if got := abbreviate(c.s, c.n); got != c.want {
			t.Errorf("abbreviate(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

func TestQuoteIfNumeric(t *testing.T) {
	if got := quoteIfNumeric("abc123def456abc123def456abc123def456abcd"); got != "abc123def456abc123def456abc123def456abcd" {
		t.Errorf("hex SHA must not be quoted, got %q", got)
	}
	if got := quoteIfNumeric("1234567890123456789012345678901234567890"); got != `"1234567890123456789012345678901234567890"` {
		t.Errorf("all-digit SHA must be quoted to stay a string, got %q", got)
	}
}

// fixture reproduces the bug class the user flagged: two modules share a repo
// URL but hold DIFFERENT commits (one already frozen to a short SHA, one on a
// branch). A URL-keyed rewrite clobbers the frozen one; a per-module rewrite
// must not. It also exercises blank lines, 4-space indent, inline comments, a
// .git url suffix, and a commented-out commit — all to be preserved.
const sharedURLFixture = "name: demo\n" +
	"\n" +
	"stages:\n" +
	"    - id: NORM\n" +
	"      modules:\n" +
	"          - id: nr-scrapper\n" +
	"            repository:\n" +
	"                url: https://github.com/omni-scrna/scrapper\n" +
	"                commit: 18843a7        # already frozen — must stay put\n" +
	"          - id: nr-scanpy\n" +
	"            repository:\n" +
	"                url: https://github.com/omni-scrna/scanpy\n" +
	"                commit: harmonize-argparsing\n" +
	"\n" +
	"          #   commit: graphs\n" +
	"    - id: PCA\n" +
	"      modules:\n" +
	"          - id: pc-scrapper\n" +
	"            repository:\n" +
	"                url: https://github.com/omni-scrna/scrapper.git\n" +
	"                commit: main\n"

func TestApplyResultsPerModule(t *testing.T) {
	const shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	dir := t.TempDir()
	path := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(path, []byte(sharedURLFixture), 0644); err != nil {
		t.Fatal(err)
	}

	// Results in document order: nr-scrapper, nr-scanpy, pc-scrapper.
	results := []PinResult{
		{ModuleID: "nr-scrapper", OldSHA: "18843a7", NewSHA: "18843a7", Status: StatusAlreadyPinned},
		{ModuleID: "nr-scanpy", OldSHA: "harmonize-argparsing", NewSHA: shaA, Status: StatusPinned},
		{ModuleID: "pc-scrapper", OldSHA: "main", NewSHA: shaB, Status: StatusPinned},
	}
	if err := applyResults(path, results); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, path)
	want := strings.NewReplacer(
		"commit: harmonize-argparsing", "commit: "+shaA,
		"commit: main", "commit: "+shaB,
	).Replace(sharedURLFixture)

	if got != want {
		t.Fatalf("applyResults not surgical / per-module.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// Belt-and-braces on the regression itself: the frozen sibling is intact.
	if !strings.Contains(got, "commit: 18843a7        # already frozen — must stay put") {
		t.Errorf("frozen sibling sharing the scrapper URL was clobbered:\n%s", got)
	}
	if !strings.Contains(got, "#   commit: graphs") {
		t.Errorf("commented-out commit was disturbed:\n%s", got)
	}
}

func TestApplyResultsNoChangesLeavesFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(path, []byte(sharedURLFixture), 0644); err != nil {
		t.Fatal(err)
	}
	// Everything already-pinned / unchanged: New == Old for every module.
	results := []PinResult{
		{ModuleID: "nr-scrapper", OldSHA: "18843a7", NewSHA: "18843a7", Status: StatusAlreadyPinned},
		{ModuleID: "nr-scanpy", OldSHA: "harmonize-argparsing", NewSHA: "harmonize-argparsing", Status: StatusUnchanged},
		{ModuleID: "pc-scrapper", OldSHA: "main", NewSHA: "main", Status: StatusUnchanged},
	}
	if err := applyResults(path, results); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != sharedURLFixture {
		t.Errorf("file changed despite no per-module changes:\n%s", got)
	}
}

func TestApplyResultsRejectsStructureMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(path, []byte(sharedURLFixture), 0644); err != nil {
		t.Fatal(err)
	}
	// 3 repositories in the file, but only 1 result — must refuse, not corrupt.
	err := applyResults(path, []PinResult{{ModuleID: "x", OldSHA: "a", NewSHA: "b"}})
	if err == nil || !strings.Contains(err.Error(), "structure changed") {
		t.Fatalf("expected a structure-mismatch error, got %v", err)
	}
	if got := readFile(t, path); got != sharedURLFixture {
		t.Errorf("file was modified despite the guard firing:\n%s", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
