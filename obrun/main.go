package main

import (
	"fmt"
	"os"

	"github.com/btraven00/obflow/cmd"
)

func main() {
	c := cmd.NewRunCmdProd()
	c.Use = "obrun"
	c.SilenceUsage = true
	c.Short = "a no-frills ob runner"
	c.Long = `Runs the configured omnibenchmark against its canonical plan.

If the benchmark's top-level software_backend is "conda", obrun generates
a pixi manifest at .obflow/pixi.toml and runs via ` + "`pixi run`" + `.
Otherwise it runs via ` + "`uv tool run`" + `.

Extra arguments after -- are passed through to snakemake (via ob run).`
	c.AddCommand(cmd.NewUseCmd())
	if err := c.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
