An opinionated [omnibenchmark](https://docs.omnibenchmark.org) set of tools.

## obrun

A no-frills `ob` runner.

Grab the prebuilt binary from [releases](https://github.com/btraven00/obflow/releases/tag/nightly) and drop it on your `PATH`.

```sh
obrun use ~/lab/some-bench/bench.yaml   # remember the active plan
obrun                                   # ob run <plan>
obrun -- --cores 8                      # extra args pass through to ob
```


You'll need one of:

- [`pixi`](https://pixi.sh) — for the `conda` software backend.
- [`uv`](https://docs.astral.sh/uv/) — for `apptainer`, `podman`, or `envmodules`.

## obflow

Intended for developers, to prevent [repetitive strain
injuries](https://www.nhs.uk/conditions/repetitive-strain-injury-rsi/) from
editing too many YAML files.

Manages a benchmark and its module repos as a workspace of sibling
clones, driven by the benchmark YAML. See [`docs/`](docs/) (build with
`mkdocs serve`) for install, quickstart, and full command reference.

## Configuration

`obrun use` writes `./.obflow/config.yaml`, the same file `obflow use`
writes, so the two tools share state.
