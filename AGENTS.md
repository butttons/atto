# atto

Headless, snapshot-guarded file editing. A Go binary in `tools/atto`, a pi extension
in `tools/atto/pi`, and a contract in `tools/atto/CONTRACT.md`. What it is and why
is in `README.md`; how the format works is in the contract.

## Map

| Path | What |
|---|---|
| `tools/atto/*.go` | the binary — stdlib only, no dependencies |
| `tools/atto/pi/atto.ts` | the pi extension: gates the model onto atto |
| `tools/atto/pi/guard.ts` | the shell guard, a pure predicate |
| `tools/atto/CONTRACT.md` | the five invariants, plan grammar, guarantees, benchmarks |
| `tools/atto/bench/` | two harnesses: `bench.py` quick, `tools_bench.py` real CLIs |
| `.agents/skills/atto/` | the skill a model loads before editing a file |

## Build and test

```sh
cd tools/atto
go build -o atto .
go test ./...
go vet ./...
npm run --prefix ../.. test:guard     # shell guard, 27 cases, no model involved
```

Benchmarks:

```sh
python3 tools/atto/bench/bench.py --atto tools/atto/atto
python3 tools/atto/bench/tools_bench.py --atto "$PWD/tools/atto/atto" --hashline PATH --snapedit-cli PATH
```

## Two things that waste a session here

1. **Editing this checkout does not update the installed extension.** pi loads the
   clone at `~/.pi/agent/git/github.com/butttons/atto`. Extension changes need
   commit, push, then `pi update git:github.com/butttons/atto`. Go changes only need
   `go build -o tools/atto/atto .` — `~/.local/bin/atto` is a symlink to it.
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
- Never commit the built binary; `tools/atto/atto` is ignored.
- Files under 800 non-test lines. Comments say why, not what. No emoji.
- A shell-guard fix needs a test case in `guard.test.ts`. Every case in that file is
  a false positive or a bypass that actually happened; keep it that way.
