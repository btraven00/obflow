# Commands

Pass `--help` to any command for full flag reference.

## Basics

| Command | Purpose |
|---|---|
| `obflow use <plan>` | Set default plan in `./.obflow/config.yaml`. Walks up parents like `.git`. |
| `obflow init [--parent DIR]` | Clone modules as siblings; write `.obflow.lock`. |
| `obflow status` | Active plan, mode, local/canonical divergence, per-module branch + dirty flag. |
| `obflow run [--prod] [-- ob-args]` | Invoke `ob run` via `uv` (or `pixi` for conda backend). |
| `obflow dev [--fork]` | Write `bench.local.yaml`; switch clean modules to `origin/HEAD`; with `--fork`, ensure a `fork` remote per module. |
| `obflow list [--origin]` | Print modules. Reads local YAML in dev mode; `--origin` reads canonical. |

## Git fan-out

| Command | Purpose |
|---|---|
| `obflow checkout <branch> [-b]` | Concerted checkout (`-b` to create). |
| `obflow pull` | `git pull --ff-only` per module. |
| `obflow push` | Push current branch to `fork` if present else `origin`; skip clean modules. |
| `obflow foreach -- <cmd>` | Run a shell command in every module dir. |
| `obflow trim [--branch NAME] [--force]` | Delete local branches merged into `origin/HEAD`. |

## Plan YAML (`obflow plan ...`)

| Command | Purpose |
|---|---|
| `obflow plan fmt [path] [--local]` | Reformat a plan YAML in place (preserves comments). |
| `obflow plan pin [--ref REF]` | Rewrite canonical commit SHAs from `origin/<ref>` using local clones. Default ref: `origin/HEAD`. |
| `obflow plan pin --remote [--ref REF]` | Same, but resolve SHAs over the network with `git ls-remote` — no clones, no lock. Without `--ref`, dereferences each module's current branch in place. Surgical: edits only the commit lines. CI-friendly (used by the pin-commits bot). |
| `obflow plan promote` | Copy local YAML edits back into canonical (urls/commits restored). |

## Configuration

`obflow config` reads/writes `./.obflow/config.yaml` git-config-style.

| Key | Meaning |
|---|---|
| `default.plan` | Path to the active benchmark YAML. Set by `obflow use`. |
| `omnibenchmark.pr` | PR number on `omnibenchmark/omnibenchmark` to install (highest priority). |
| `omnibenchmark.branch` | Branch on `omnibenchmark/omnibenchmark` to install. |
| `omnibenchmark.version` | Pinned PyPI version. |

```sh
obflow config                              # list everything
obflow config omnibenchmark.branch         # print value
obflow config omnibenchmark.branch dev     # set value
obflow config --unset omnibenchmark.branch # clear
```

Resolution priority for `omnibenchmark.*`: `pr > branch > version > pypi-latest`.
