# How-to

Task-oriented recipes. Each assumes you've run `obflow use <plan>` and
`obflow init`.

## Test a PR of omnibenchmark

Configure once, then `obflow run` uses the PR build:

```sh
obflow config omnibenchmark.pr 123
obflow run
```

When done, clear it:

```sh
obflow config --unset omnibenchmark.pr   # back to latest pypi
```

Resolution priority is `pr > branch > version > pypi-latest`. Setting
any of `branch`/`version` follows the same pattern.

## Test a PR on a benchmark module

`obflow` doesn't wrap PR fetching; do it per-module with `gh`:

```sh
cd ../some-bench-modules/<module>
gh pr checkout 456
cd -
obflow dev          # regenerate bench.local.yaml with the new branch
obflow run          # runs against the checked-out PR
```

`obflow status` will show the module on the PR's branch.

## Edit a YAML stage and push it back to canonical

Sketch in `bench.local.yaml` (uncomment a stage, tweak parameters,
whatever). When ready:

```sh
obflow status                # confirms divergence + hint
obflow plan promote          # writes canonical from local (urls/commits restored)
git -C $(dirname <plan>) diff   # review
```

`plan promote` refuses to write canonical if any local module has a
url that isn't a known remote (e.g. you added a new module without a
github URL — fix that first).

## Update canonical after PRs merge upstream

```sh
obflow pull            # fast-forward each module on its current branch
obflow plan pin        # rewrite canonical SHAs from origin/HEAD
obflow trim            # clean up merged local branches
```

`plan pin` is upstream-only (`git fetch origin && rev-parse origin/<ref>`),
so the SHAs it writes are always fetchable by anyone.

## Pin / unpin a plan without a workspace (CI / no clones)

```sh
obflow plan pin --remote                  # freeze: deref each module's branch -> SHA
obflow plan pin --remote --ref my-branch  # freeze every module to my-branch's SHA
obflow plan track my-branch               # thaw: point modules at my-branch (where it exists)
```

Both resolve over the network with `git ls-remote`, so they need neither sibling
clones nor a lock file, and both edit only the `commit:` lines — the rest of the
YAML stays byte-for-byte intact (minimal-diff PRs).

- `pin --remote` **never re-pins a module already at a SHA** — a deliberate
  freeze is never silently moved (reported `already-pinned`).
- `track <branch>` only switches modules whose remote actually has that branch;
  the rest are left alone (reported `not-found`).

Add `--json` to get `{"modules":[{module,url,old,new,status,detail}]}` for
scripting / bots.

These are what the **obflow bot** runs. In a benchmark repo, comment:

- `/pin-commits [ref]` — freeze module commits.
- `/track-branch <branch>` — move modules onto a branch.

On a PR the change is committed onto that PR's branch; on an issue it opens a
dedicated PR. See `.github/workflows/obflow-bot.yml` and its `README.md`.

## Use your own forks instead of pushing to upstream

```sh
obflow dev --fork      # creates a `fork` remote per module via `gh repo fork`
obflow checkout feat-x -b
# ... edit, commit ...
obflow push            # auto-targets `fork` (falls back to `origin`)
```

The canonical YAML never references your forks; they're per-developer
plumbing.

## Branch across all modules at once

```sh
obflow checkout feat-x -b   # creates feat-x in every module
obflow status               # confirm
```

`obflow` deliberately creates the branch in every module, even ones you
may not touch. After the feature ships, `obflow trim` removes empty/merged
branches in one shot.

## Run an arbitrary command across all modules

```sh
obflow foreach -- git log -1 --oneline
obflow foreach -- pre-commit run --all-files
```

Per-repo prefix on each line; runs in parallel.

## Bring in canonical changes after someone else updates the spec

```sh
git pull              # in the canonical bench repo (you own it)
obflow init             # clones any new modules; existing clones untouched
obflow dev              # regenerate bench.local.yaml
```

## Fix a malformed plan YAML

```sh
obflow plan fmt         # in dev mode, formats local; otherwise canonical
obflow plan fmt --local # explicit
```

Requires the file to parse first. Parse errors are shown cargo-style with
context lines and a caret.

## See where I am

```sh
obflow status
```

Shows the active plan, dev/prod mode, local-edit divergence summary
(with hint to `obflow plan promote`), and per-module branch + dirty flag.

## Iterate on a single module

When debugging one module's logic (e.g. tweaking `pca.py`), `obflow run`
+ Snakemake is too coarse. `obflow enter` drops you into a pixi shell
scoped to that module, with upstream inputs from the latest
successful run preloaded as env vars and a `runit` helper on PATH that
echoes-then-runs the module's entrypoint with all required flags
prefilled:

```sh
obflow enter pca-scanpy
# inside the shell — plan-declared parameters are used as defaults:
runit
# + python pca.py --output_dir /tmp/... --name datasets \
#     --normalized.h5 /.../datasets_normalized.h5 \
#     --selected.genes /.../datasets_selected.txt.gz \
#     --pca_type scanpy_arpack --n_components 50 --random_seed 42
# (then it runs)

# override any plan default by passing it after `runit`:
runit --pca_type scanpy_randomized --random_seed 7
```

`runit` reads the **first** entry of the module's plan-declared
`parameters:` block; for cartesian expansions like
`selection_type: [a, b]`, it picks the first value. User-supplied flags
override (argparse takes the last occurrence).

You can also use the env vars directly:

```sh
echo $NORMALIZED_H5      # /.../datasets_normalized.h5
echo $SELECTED_GENES     # /.../datasets_selected.txt.gz
echo $OBMR_OUTPUT_DIR    # /tmp/obflow-enter-pca-scanpy-<rand>
python pca.py --output_dir $OBMR_OUTPUT_DIR --name $OBMR_NAME \
  --normalized.h5 $NORMALIZED_H5 --selected.genes $SELECTED_GENES \
  --pca_type scanpy_arpack --n_components 50 --random_seed 0
```

Env-var names are derived from the plan's input ids (e.g.
`normalized.h5` → `NORMALIZED_H5`). The upstream chain is anchored on
the deepest matching candidate so all inputs come from one coherent
DAG path.

If you don't want a sub-shell, source the exports into your current
shell:

```sh
eval "$(obflow enter pca-scanpy --print)"
runit --pca_type scanpy_arpack --n_components 50 --random_seed 0
```

To debug interactively, add `ipython` / `ipdb` to the module's
`pixi.toml` and pass `--env <name>` if you put them under a feature.

### Requirements

- The benchmark must be in dev mode (run `obflow dev` first — `obflow enter`
  loads the local YAML).
- At least one successful prior `obflow run` so the upstream stages have
  produced files under `out/`. If a producer has never run, `obflow enter`
  errors with the missing input id and a pointer to `obflow run`.
- The module needs a `pixi.toml` (its environment) and an
  `omnibenchmark.yaml` declaring its entrypoints (so `runit` can
  resolve the correct script).
