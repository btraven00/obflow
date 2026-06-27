package cmd

import (
	"github.com/btraven00/obflow/internal/workspace"
	"github.com/spf13/cobra"
)

func newTrackCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "track <branch> [bench.yaml]",
		Short: "Point each module at <branch>, but only where that branch exists (in place)",
		Long: "The inverse of `pin`: set repository.commit to <branch> for every module\n" +
			"whose remote actually has it. Modules lacking the branch are left untouched\n" +
			"(reported `not-found`). Resolves over the network with `git ls-remote` — no\n" +
			"clones, no lock.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			branch := args[0]
			var planArg string
			if len(args) > 1 {
				planArg = args[1]
			}
			plan, err := resolvePlan(planArg)
			if err != nil {
				return err
			}
			results, err := workspace.TrackBranch(plan, branch)
			if err != nil {
				return err
			}
			return printResults(results, asJSON)
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit a JSON report instead of a table")
	return c
}
