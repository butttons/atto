# atto

Snapshot-guarded file editing for agents: a Go binary and a pi extension.

Every change to an existing file is one atomic transaction against a rev the
agent was given. Read the lines, hold the rev, send only the new text, get a diff
and a new rev back. Nothing is retyped, nothing fuzzy-matches, and a stale rev
writes nothing.

## Install

Prebuilt binaries for every release: macOS and Linux, amd64 and arm64, each
attached with a `SHA256SUMS`. Windows is not built (the file lock is unix-only).

```sh
os=$(uname -s | tr 'A-Z' 'a-z')                          # darwin or linux
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
curl -fsSLO "https://github.com/butttons/atto/releases/latest/download/atto_${os}_${arch}.tar.gz"
curl -fsSLO "https://github.com/butttons/atto/releases/latest/download/SHA256SUMS"

# check the download before unpacking it
grep "atto_${os}_${arch}\.tar\.gz" SHA256SUMS > expected.sha256
sha256sum -c expected.sha256 2>/dev/null || shasum -a 256 -c expected.sha256

tar -xzf "atto_${os}_${arch}.tar.gz" atto
install -m 0755 atto ~/.local/bin/atto        # or /usr/local/bin with sudo
atto version
```

From source, Go 1.22+ and standard library only:

```sh
go install github.com/butttons/atto/cmd/atto@latest   # into $GOBIN
# or, in a checkout:
go build -o atto ./cmd/atto
```

## Commands

```sh
atto read FILE [FROM:TO]   # numbered lines + rev (a whole-file read stops at 2,000 lines)
atto find FILE TEXT        # matching lines + rev (literal, case-sensitive, 200 matches)
atto stat FILE             # rev, line count, byte count; no lines
atto sub FILE --rev R      # replace exact text: '-'/'+' blocks on stdin
atto patch < plan          # line-addressed plan on stdin
atto undo FILE             # revert the last transaction, if the file still matches
atto log [FILE]            # the last 20 transactions
atto version
```

Exit 0 on success, 1 on failure, 2 on usage. Data goes to stdout, diagnostics to
stderr.

## Replacing text

`atto sub` is the common case: the lines to find, then the lines to write.

```sh
atto read conf.ini

atto sub conf.ini --rev 8f3a1c2d9e0b <<'ATTO'
-retries=3
+retries=5
ATTO
```

The anchor is the text, so no line arithmetic is needed, and the rev is still the
guard. `@@` separates blocks, and every block in one call is one transaction.

| the text matches | default | with a flag |
|---|---|---|
| nowhere | refused | refused |
| once | replaced | replaced |
| several times | refused, with the line numbers | `--expect N` or `--all` |

That count is the difference from a script, whose `replace()` silently does
nothing when the text is absent and silently replaces every copy when it is not.
`--dry` prints the diff and writes nothing.

## Line spans

When the change is genuinely a position — moving a block, deleting lines,
inserting at a line — address the span:

```sh
atto read bench/fixtures/sample.rs 1:6

atto patch <<'ATTO'
file bench/fixtures/sample.rs
rev 8f3a1c2d9e0b
SWAP 3.=3:
+        return 1;
ATTO
```

`SWAP A.=B` replaces lines A..B inclusive, `DEL A.=B` deletes,
`INS.PRE`/`INS.POST` insert around a line, `INS.HEAD`/`INS.TAIL` insert at the
ends. Body lines start with `+`; a literal leading `+` is written `++`. One file
and one rev per plan, and hunks must not overlap.

If the file moved since the rev, the hunk is relocated by its old text plus three
lines of context: a unique match is applied, no match or several matches is a
conflict, and a conflict writes nothing. It never fuzzy-matches.

[CONTRACT.md](CONTRACT.md) is the full contract: invariants, grammar, guarantees.

## Install the pi extension

The binary works on its own — any agent can call it from a shell. The extension
is what turns it into the default edit path in pi. Install the binary first, then
the package (Node 22+ is needed only to run the guard tests):

```sh
pi install git:github.com/butttons/atto
```

The extension probes `atto version` at session start and, when that fails,
warns and leaves the gate off: `edit`, `write`, and
shell edits behave as stock pi. That is deliberate. With no binary there is
nothing to route an edit to, and blocking `edit` anyway would strand the model.

`pi install` clones into `~/.pi/agent/git/github.com/butttons/atto`, and that
clone is what pi loads: a change here reaches it only through
`pi update git:github.com/butttons/atto`.

The extension registers `atto_read`, `atto_find`, `atto_sub`, `atto_edit` and
`atto_undo`, and blocks `edit`, `write` on an existing file, and shell file
editing. New files are still `write`. [pi/README.md](pi/README.md) covers the
gate, what a rev authorizes, `/atto-stats`, and what it does not stop.

## State

`$ATTO_HOME` (default `$XDG_STATE_HOME/atto`, else `~/.local/state/atto`) holds:

- `snapshots/<rev>` — content-addressed images, needed for relocation and undo
- `journal.jsonl` — every transaction, which is what `atto log` and `undo` read

Nothing is created until a command needs it.

## Build, test, benchmark

```sh
go build -o atto ./cmd/atto
go test ./... && go vet ./...
npm test                  # the three pi modules, 57 cases, no model involved (node 22+)
```

```sh
python3 bench/bench.py --atto "$PWD/atto"
python3 bench/tools_bench.py --atto "$PWD/atto" --hashline PATH --snapedit-cli PATH
```

[AGENTS.md](AGENTS.md) is the entry point for an agent working in this repo.
[`.agents/skills/atto/SKILL.md`](.agents/skills/atto/SKILL.md) is the skill that
teaches a model to edit through atto.
