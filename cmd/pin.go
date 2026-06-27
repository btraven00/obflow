package cmd

import (
	"fmt"

	"github.com/btraven00/obflow/internal/workspace"
	"github.com/spf13/cobra"
)

func newPinCmd() *cobra.Command {
	var ref string
	var remote bool
	var asJSON bool
	var abbrev int
	c := &cobra.Command{
		Use:   "pin [bench.yaml]",
		Short: "Freeze each module's tracked branch to a concrete commit SHA (in place)",
		Long: "Rewrite each module's repository.commit to a concrete SHA.\n\n" +
			"Default: resolve origin/<ref> in each module's local clone (needs a workspace + lock).\n" +
			"--remote: resolve over the network with `git ls-remote` (no clones/lock; CI-friendly).\n\n" +
			"A module already at a SHA is never re-pinned (reported `already-pinned`).\n" +
			"SHAs are abbreviated to --abbrev chars to match the plan convention.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			plan, err := resolvePlan(firstArg(args))
			if err != nil {
				return err
			}

			var results []workspace.PinResult
			if remote {
				results, err = workspace.PinRemote(plan, ref, abbrev)
			} else {
				var lock *workspace.Lock
				lock, _, err = loadLock(plan)
				if err != nil {
					return err
				}
				results, err = workspace.Pin(plan, lock, ref, abbrev)
			}
			if err != nil {
				return err
			}
			return printResults(results, asJSON)
		},
	}
	c.Flags().StringVar(&ref, "ref", "", "ref to pin from (default: each module's current commit; clone mode uses origin's HEAD)")
	c.Flags().BoolVar(&remote, "remote", false, "resolve SHAs over the network via git ls-remote (no clones/lock; CI-friendly)")
	c.Flags().BoolVar(&asJSON, "json", false, "emit a JSON report instead of a table")
	c.Flags().IntVar(&abbrev, "abbrev", 7, "abbreviate written SHAs to N hex chars (0 = full 40-char SHA)")
	return c
}

// printResults renders per-module pin/track outcomes as a table, or as JSON
// (the machine-readable form the CI bot consumes) when asJSON is set.
func printResults(results []workspace.PinResult, asJSON bool) error {
	if asJSON {
		b, err := workspace.ResultsJSON(results)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	for _, r := range results {
		line := fmt.Sprintf("%-20s %-15s", r.ModuleID, r.Status)
		switch r.Status {
		case workspace.StatusPinned, workspace.StatusTracking:
			line += fmt.Sprintf("%s -> %s", disp(r.OldSHA), disp(r.NewSHA))
		case workspace.StatusAlreadyPinned, workspace.StatusUnchanged:
			line += fmt.Sprintf("(%s)", disp(r.NewSHA))
		}
		if r.Warning != "" {
			line += "  " + r.Warning
		}
		fmt.Println(line)
	}
	return nil
}

// disp shortens git SHAs for display but leaves branch/tag names intact.
func disp(s string) string {
	if looksLikeSHA(s) {
		return s[:7]
	}
	return s
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
