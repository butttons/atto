# atto

Headless, snapshot-guarded file editing: a Go binary and a pi extension. What it is
and why is in `README.md`; the format is in `CONTRACT.md`.

## Map

| Path | What |
|---|---|
| `*.go` | the binary — `package main` at the module root, stdlib only |
| `pi/atto.ts` | the pi extension: gates the model onto atto |
| `pi/guard.ts` | the shell guard, a pure predicate |
| `CONTRACT.md` | the five invariants, plan grammar, guarantees, benchmarks |
| `bench/` | two harnesses: `bench.py` quick, `tools_bench.py` real CLIs |
| `.agents/skills/atto/` | the skill a model loads before editing a file |

No `cmd/` and no `internal/`: there is one binary and no importable library, so the
module root is the main package.

## Build and test

```sh
go build -o atto .    # also refreshes ~/.local/bin/atto, which symlinks here
go test ./...
go vet ./...
npm run test:guard   # shell guard, 27 cases, no model involved
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
   `go build -o atto .` — `~/.local/bin/atto` is a symlink to it. Deleting or
   moving that binary breaks every atto tool in the session until you rebuild.
2. **Loading the installed package and a local `--extension` at once is a hard
   error**, `Tool "atto_read" conflicts with ...`. Pass `--extension` only where the
   package is not installed.

Also: extensions load at session start, so a guard change never takes effect in the
session that made it. Prove it with a fresh headless run
(`pi --print --mode json --no-session ...`), not by reasoning.

## Rules

- One mutation path: `atto read` -> snapshot rev -> `atto patch`.
- Address text by 1-based line spans, never by byte offsets.
- Keep the pi gate closed: no `edit`, no `write` on existing files, no Python
  or sed file edits.
- Never commit the built binary; `/atto` is ignored.
- Files under 800 non-test lines. Comments say why, not what. No emoji.
- A shell-guard fix needs a test case in `guard.test.ts`. Every case in that file is
  a false positive or a bypass that actually happened; keep it that way.
