package cmd

import "github.com/spf13/cobra"

func newPlanCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "plan",
		Short: "Operate on the benchmark plan YAML (fmt / pin / promote)",
	}
	c.AddCommand(
		newFmtCmd(),
		newPinCmd(),
		newTrackCmd(),
		newPromoteCmd(),
	)
	return c
}
