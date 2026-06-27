package workspace

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestRewriteCommitLinesIsSurgical(t *testing.T) {
	// 4-space-deep indentation, blank lines, inline comments, a commented-out
	// commit, and a .git url suffix — all must survive untouched except the two
	// live commit values.
	src := "name: demo\n" +
		"\n" +
		"stages:\n" +
		"    - id: norm\n" +
		"      modules:\n" +
		"          - id: scanpy            # primary normaliser\n" +
		"            repository:\n" +
		"                url: https://github.com/omni-scrna/scanpy\n" +
		"                commit: harmonize-argparsing  # branch under review\n" +
		"                entrypoint: normalize\n" +
		"\n" +
		"          # - id: old\n" +
		"          #     commit: graphs\n" +
		"          - id: scrapper\n" +
		"            repository:\n" +
		"                url: https://github.com/omni-scrna/scrapper.git\n" +
		"                commit: harmonize-argparsing\n"

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(src), &root); err != nil {
		t.Fatal(err)
	}
	// Note the .git suffix on the second url: normRemote must still match.
	got := string(rewriteCommitLines([]byte(src), &root, map[string]string{
		"https://github.com/omni-scrna/scanpy":   "1111111111111111111111111111111111111111",
		"https://github.com/omni-scrna/scrapper": "abc123def456abc123def456abc123def456abcd",
	}))

	want := strings.NewReplacer(
		"commit: harmonize-argparsing  # branch under review",
		`commit: "1111111111111111111111111111111111111111"  # branch under review`,
		"                commit: harmonize-argparsing\n",
		"                commit: abc123def456abc123def456abc123def456abcd\n",
	).Replace(src)

	if got != want {
		t.Errorf("rewrite not surgical.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// The commented-out `#     commit: graphs` must be left alone.
	if !strings.Contains(got, "#     commit: graphs") {
		t.Errorf("commented-out commit was disturbed\n%s", got)
	}
}
