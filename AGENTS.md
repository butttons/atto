# atto

Snapshot-guarded file editing: a Go binary and a pi extension. The what and why
are in `README.md`; the format is in `CONTRACT.md`.

## Map

| Path | What |
|---|---|
| `cmd/atto/` | `package main` — argument parsing and exit codes, nothing else |
| `internal/atto/` | `package atto` — the engine; `Run(args) int` is the only entry point |
| `pi/atto.ts` | the pi extension: gates the model onto atto |
| `pi/guard.ts` | the shell guard, a pure predicate |
| `pi/windows.ts` | what a rev authorizes: the lines a tool actually showed |
| `CONTRACT.md` | the five invariants, plan grammar, guarantees, benchmarks |
| `bench/` | harnesses: `bench.py` quick, `tools_bench.py` real CLIs, `ab.py` the model A/B |
| `.agents/skills/atto/` | the skill a model loads before editing a file |

Standard layout on purpose: `cmd/` holds the binary, `internal/` holds code that is
not a public API, and tests sit next to what they test.

## Fresh machine

Go 1.22+ is the only build prerequisite; there are no prebuilt binaries.

```sh
go build -o atto ./cmd/atto
mkdir -p ~/.local/bin && ln -sf "$PWD/atto" ~/.local/bin/atto
pi install git:github.com/butttons/atto
```

Binary before package. The extension probes `atto version` at session start and
leaves the gate off when it fails, so a wrong order silently degrades to stock
pi: `edit` works, nothing is guarded, and no atto tool can run.

## Build and test

```sh
go build -o atto ./cmd/atto    # ~/.local/bin/atto symlinks here
go test ./...
go vet ./...
npm test                      # both pi guards, 49 cases, no model involved
```

Benchmarks:

```sh
python3 bench/bench.py --atto "$PWD/atto"
python3 bench/tools_bench.py --atto "$PWD/atto" --hashline PATH --snapedit-cli PATH
```

## Two things that waste a session here

1. **Editing this checkout does not update the installed extension.** pi loads the
   clone at `~/.pi/agent/git/github.com/butttons/atto`. Extension changes need
   commit, push, then `pi update git:github.com/butttons/atto`. Go changes only need
   `go build -o atto ./cmd/atto` — `~/.local/bin/atto` is a symlink to it. Deleting
   that binary breaks every atto tool in the session until you rebuild.
2. **Loading the installed package and a local `--extension` at once is a hard
   error**, `Tool "atto_read" conflicts with ...`. Pass `--extension` only where the
   package is not installed.

Also: extensions load at session start, so a guard change never takes effect in the
session that made it. Prove it with a fresh headless run
(`pi --print --mode json --no-session ...`), not by reasoning.

## Releases

`.github/workflows/release.yml` runs on `release: published` and nothing else, so a
push never produces a binary. To cut one:

```sh
git tag v0.1.0
git push origin v0.1.0
gh release create v0.1.0 --generate-notes
```

Publishing builds darwin and linux for amd64 and arm64, stamps the tag into
`atto version`, and attaches the tarballs plus `SHA256SUMS`. The workflow runs
`go vet` and `go test` first, so a red suite blocks the release rather than
shipping a binary nobody tested.

## Rules

- One mutation path: `atto read` -> snapshot rev -> `atto sub` or `atto patch`.
  `sub` addresses by exact text and is the default; `patch` addresses by line span and is
  for block moves and positional inserts. Both check the rev and write atomically.
- Address text by exact content or by 1-based line spans, never by byte offsets,
  and never with an `indexOf`-then-count-lines conversion — that is `sub`'s job.
- Keep the pi gate closed: no `edit`, no `write` on existing files, no Python
  or sed file edits. The `sub` verb exists so the gate costs nothing: if a
  reach for Python is the one thing the tool set cannot express, widen the tool
  set rather than the gate.
- Never commit the built binary; `/atto` is ignored.
- Files under 800 non-test lines. Comments say why, not what. No emoji.
- A shell-guard fix needs a test case in `guard.test.ts`. Every case in that file is
  a false positive or a bypass that actually happened; keep it that way.
- A verb in the `usage` text needs a dispatch case: `TestEveryAdvertisedVerbIsDispatched`
  fails otherwise. `atto log` was advertised in five docs and reachable in none.
