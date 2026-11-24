# atto

Headless, agent-first file editing. One binary, one contract, one pi extension.

Every change to an existing file is a snapshot-guarded, line-addressed, atomic
transaction: read the lines, get a rev, send only the new text, get a diff and
a new rev. No retyping old text, no fuzzy matching, no Python heredocs.

## Install

```sh
go install ./cmd/atto    # Go 1.22+, standard library only; puts atto in $GOBIN
```

## Use

```sh
atto read FILE [FROM:TO]   # numbered lines + snapshot rev
atto stat FILE             # just the rev, ~120 bytes
atto patch < plan          # one atomic transaction
atto undo FILE             # revert the last transaction
atto log [FILE]            # recent transactions
```

Read the region you are about to change, keep the rev, then send only the new
text addressed to the rev's line span:

```sh
atto read bench/fixtures/sample.rs 1:6

atto patch <<'ATTO'
file bench/fixtures/sample.rs
rev 8f3a1c2d9e0b
SWAP 3.=3:
+        return 1;
ATTO
```

The response is a diff, the new rev, and the new span. If the file moved on, the
patch refuses and shows the contested lines.

### Plan grammar

```
file PATH
rev SNAPSHOT
SWAP A.=B:
+new line
+another
DEL C.=D
INS.POST E:
+after E
INS.TAIL:
+at the end
```

`SWAP A.=B` replaces lines A through B inclusive, `DEL` deletes, `INS.PRE`/`INS.POST`
insert around a line, `INS.TAIL` appends. Body lines start with `+`; a literal
leading `+` is `++`. One file and one rev per plan, and hunks must not overlap.

[CONTRACT.md](CONTRACT.md) is the full contract: the five invariants, the state
model, the guarantees, what is deliberately not built, and the benchmarks.

## pi extension

The extension makes pi edit through atto and blocks `edit`, `write` on existing
files, and Python/sed/perl/tee file edits. New files are still `write`.

```sh
pi install git:github.com/butttons/atto
pi update git:github.com/butttons/atto      # after a change lands here
```

Put `atto` on PATH (or set `ATTO_BIN`). The extension is declared in
`package.json` and loads from `pi/atto.ts`; [pi/README.md](pi/README.md) covers
the gate, the tool loop, `/atto-stats`, and the escape hatch.

Installing clones the extension into `~/.pi/agent/git/github.com/butttons/atto`.
That clone is what pi loads, so a change to this repo reaches it only through
`pi update`. The Go binary is separate: it is built locally and never ships
through the package.

## Build, test, benchmark

```sh
go test ./...
go vet ./...
npm run test:guard        # the shell guard, 27 cases
```

The guard is a pure predicate in `pi/guard.ts`, tested directly rather than by
driving a model at it. `bench/bench.py` is the quick five-edit comparison;
`bench/tools_bench.py` drives the real `hashline` and `snapedit` CLIs as well as
exact replace and the Python workaround: 12 tasks including typo, drift,
duplicate, stale, and a 5,000-line file, with retries counted.

```sh
go build -o atto ./cmd/atto && python3 bench/bench.py --atto "$PWD/atto"

git clone https://github.com/quangdang46/hashline && (cd hashline && cargo build --release)
git clone https://github.com/rianprei/snapedit
python3 bench/tools_bench.py --atto "$PWD/atto" --hashline PATH --snapedit-cli PATH
```

## State

`$ATTO_HOME` (default `$XDG_STATE_HOME/atto`, else `~/.local/state/atto`) holds:

- `snapshots/<rev>` — content-addressed pre/post images, needed for conflict relocation and undo
- `journal.jsonl` — every transaction, also the event stream a viewer would consume

The tool never creates the state directory until a read or patch needs it.

## For agents

[AGENTS.md](AGENTS.md) is the entry point for a coding agent working in this
repo: layout, commands, and the traps that cost a session.

[`.agents/skills/atto/SKILL.md`](.agents/skills/atto/SKILL.md) is the skill for
the other direction — a model that needs to edit a file and should use atto to
do it. It is declared under `pi.skills` in `package.json`, so installing the
package teaches the skill too.
