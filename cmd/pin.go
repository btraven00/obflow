package cmd

import (
	"fmt"

	"github.com/btraven00/obflow/internal/workspace"
	"github.com/spf13/cobra"
)

func newPinCmd() *cobra.Command {
	var ref string
	var remote bool
	c := &cobra.Command{
		Use:   "pin [bench.yaml]",
		Short: "Rewrite canonical YAML commit SHAs per module (in place)",
		Long: "Rewrite each module's repository.commit to a concrete SHA.\n\n" +
			"Default: resolve origin/<ref> in each module's local clone (needs a workspace + lock).\n" +
			"--remote: resolve over the network with `git ls-remote` (no clones/lock; CI-friendly).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			plan, err := resolvePlan(firstArg(args))
			if err != nil {
				return err
			}

			var results []workspace.PinResult
			if remote {
				results, err = workspace.PinRemote(plan, ref)
			} else {
				var lock *workspace.Lock
				lock, _, err = loadLock(plan)
				if err != nil {
					return err
				}
				results, err = workspace.Pin(plan, lock, ref)
			}
			if err != nil {
				return err
			}

			for _, r := range results {
				if r.Err != nil {
					fmt.Printf("%-20s ERROR: %v\n", r.ModuleID, r.Err)
					continue
				}
				old := disp(r.OldSHA)
				newS := disp(r.NewSHA)
				switch {
				case r.OldSHA == r.NewSHA:
					line := fmt.Sprintf("%-20s unchanged (%s)", r.ModuleID, newS)
					if r.Warning != "" {
						line += "  WARN: " + r.Warning
					}
					fmt.Println(line)
				case r.OldSHA == "":
					fmt.Printf("%-20s pinned -> %s\n", r.ModuleID, newS)
				default:
					line := fmt.Sprintf("%-20s %s -> %s", r.ModuleID, old, newS)
					if r.Warning != "" {
						line += "  WARN: " + r.Warning
					}
					fmt.Println(line)
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&ref, "ref", "", "ref to pin from (default: each module's current commit; clone mode uses origin's HEAD)")
	c.Flags().BoolVar(&remote, "remote", false, "resolve SHAs over the network via git ls-remote (no clones/lock; CI-friendly)")
	return c
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
