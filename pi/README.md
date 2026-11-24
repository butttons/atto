# atto for pi

Make pi edit through atto, and make the fallback visible.

The extension is a gate, not a request:

- registers `atto_read`, `atto_find`, `atto_sub`, `atto_edit`, `atto_undo`
- removes the built-in `edit` and `apply_patch` from the model's tool set
- blocks `write` when the target already exists (new files are still fine)
- blocks shell commands that can only mean "edit a file": `sed -i`, `perl -pi`,
  `tee`, `git apply`, `apply_patch`, `hashline patch`, and redirects into anything
  except `/tmp`, `target/`, and `/dev/*`. A redirect after `cd /tmp`, a `2>&1`,
  `=>`, `>=`, and a `>` inside quotes are not edits
- refuses an edit to lines this session never showed the model, so a rev cannot
  authorize a region the model only remembers
- logs every edit, failure, refusal, and blocked fallback to `$ATTO_HOME/pi.jsonl`,
  so "is the agent actually using it?" is a number, not a hope

The guard classifies commands and operators, never a program's text. An earlier
version tried to read `python3 -c` and heredocs to decide whether the code could
write; it produced more false positives than catches, and its bypass was one line
long. See "What this does not stop".

## Build and load

```sh
go build -o atto ./cmd/atto

ATTO_BIN="$PWD/atto" pi --extension "$PWD/pi/atto.ts" \
  --tools read,bash,grep,find,ls,atto_read,atto_find,atto_sub,atto_edit,atto_undo
```

`--tools` with plain names replaces the default set, so list the read-only tools
you still want. The extension also removes `edit`/`apply_patch` on
`session_start`, so the gate holds even when the tool list is not pinned.

## Install as a pi package

Build the binary first, then install the package:

```sh
go build -o atto ./cmd/atto && ln -sf "$PWD/atto" ~/.local/bin/atto

pi install git:github.com/butttons/atto
pi update git:github.com/butttons/atto     # after a change lands
```

The order is not cosmetic. On `session_start` the extension probes
`atto version`; when it fails it warns and leaves the gate off, so `edit`,
`write`, and shell edits behave as stock pi. Blocking `edit` with no binary to
route to would leave the model unable to change a file at all.

Installing clones into `~/.pi/agent/git/github.com/butttons/atto`, and `pi update`
is the only thing that refreshes that clone. Loading the package and a local
`--extension` at once is a hard error (`Tool "atto_read" conflicts with ...`), so
pass `--extension` only where the package is not installed.

## The loop the model is taught

1. `atto_find(path, text)` or `atto_read(path, from, to)` — numbered lines and a rev.
2. `atto_sub(path, rev, changes)` — `[{old, new}]` text replacement. This is the
   default verb: most edits are "replace this text with that text", and a model
   reaches for a Python script when the tool list cannot say it. `expect: N` or
   `all: true` states how many matches are intended; `dry: true` reports only.
3. `atto_edit(path, rev, changes)` — `[{start, end, text}]` hunks, for a change that
   is genuinely a line range: moving a block, deleting lines, inserting by position.
4. The result is a diff, a new rev, and the new line spans.

`end = start - 1` inserts before `start`. Empty `text` deletes `start..end`. A
conflict writes nothing and returns the contested lines; the model re-reads and
retries.

## What a rev authorizes

A rev proves the file has not moved. It does not prove the model looked at the lines
it is changing, and a model can write a valid patch for the wrong place: addresses
held in memory drift after the first edit, and a one-line read still returns a
whole-file rev.

So the extension remembers what it has shown, per path and rev, and refuses
anything else:

| The model did | It may then change |
|---|---|
| `atto_read(path)` (whole file, not truncated) | anything |
| `atto_read(path, 40, 50)` | 40..50, or insert at either edge |
| `atto_read(path)` on a 5,000-line file | 1..2000, the lines actually printed |
| `atto_find(path, text)` | the lines it matched |
| `atto_sub(...)` | the spans it just wrote |
| `atto_edit(...)` | the spans it just wrote |
| `atto_undo(path)` | nothing, until the file is read again |
| nothing | nothing |

A refusal writes nothing and says which lines were shown, so recovering costs one
`atto_read` or `atto_find` rather than a guess. This is per-session on purpose: a
fresh session has shown nothing, so it reads before it edits.

## Check that it is working

```
/atto-stats
```

```
atto reads:   12
atto edits:   9
atto undos:   0
blocked:      2
failed edits: 0
refused:      1
log:          ~/.local/state/atto/pi.jsonl
```

The target is `blocked: 0`, `refused: 0` and `failed edits: 0` after a few real
tasks. A blocked count is the shell guard catching a fallback the model tried; a
refusal is the window gate catching an edit to lines it never read. Both are the
tool working, and both name their reason in the log. Repeated refusals mean the
prompt guidance is not landing, not that the gate is wrong.

## Guard tests

Both guards are pure modules, tested directly instead of by driving a model at
them:

```sh
npm test          # guard.test.ts and windows.test.ts, 49 cases, no model involved
```

`guard.test.ts` is false positives and bypasses that actually happened: a scratch
redirect after `cd /tmp`, `2>&1`, `=>`, `>=`, a quoted `>` and `/dev/null` must
pass; `sed -i`, `tee`, `perl -pi`, `git apply`, and a write outside scratch must
not.

`windows.test.ts` covers the addressing gate: a span inside one region, a span
spanning a gap, a one-line read that does not authorize a block, an insertion at
the edge of the shown range, a stale rev, and reading `truncated 2000/5000` as
`1..2000`.

## What this does not stop

The gate is not a sandbox. It removes the tools and the habitual routes, and it
refuses edits to lines the model never read. It does not stop a model that decides
to write a file from inside a program:

```sh
python3 -c "open('main.go','w').write('...')"   # allowed
python3 - <<'PY'                                 # allowed
open('main.go','w').write('...')
PY
```

That is a deliberate trade. Classifying a program's behaviour with a regex cost six
false positives in one session — a heredoc used to *read* JSON, a read of a
`.github/workflows/` path, a `(cd dist && ...)` subshell — and the bypass was
always one line away. The remaining rules fire on commands that have no other
meaning, so they do not misfire on ordinary work.

What is left is the honest claim: atto is the path of least resistance, and
`atto_read`/`atto_sub`/`atto_edit` are the only tools that can change an existing
file. A model that goes around it is visible in the log rather than impossible.

`ATTO_ALLOW_SHELL_EDITS=1` disables the shell guard, for a migration or a one-off
script. Not for agent work.

## What the A/B measured

`bench/ab.py` runs the same tasks through the same model twice: stock pi
(`read,bash,edit,write`) and gated pi
(`read,bash,atto_read,atto_find,atto_sub,atto_edit,atto_undo`). Success is the file
matching the expected bytes. Every run gets a fresh directory, a fresh session, no
context files, skills, or MCP, and a throwaway config directory, so the only
difference is the editing tools.

```sh
python3 bench/ab.py --runs 3 --provider opencode-go --model glm-5.3-flash
```

The run that produced `bench/ab-results-sub.json` went after the operations that
tempted an agent into writing a Python script: replace every copy of a line, three
scattered changes in one pass, a two-line block swapped for three lines, the third
of three identical lines, a typo in one of two similar words, and an exact block
deletion. 6 tasks x 3 runs x 2 conditions:

| | stock | atto |
|---|---|---|
| tasks landed | 18/18 | 18/18 |
| tool calls, total | 47 | 43 |
| input tokens | 106,867 | 132,212 (+24%) |
| output tokens | 2,602 | 2,639 |
| mean wall time | 5.4 s | 6.0 s |
| shell fallbacks | 1 | 0 |
| tool errors | 0 | 2 |

The one shell fallback in the run is stock's, on *replace every occurrence*: `read`
then a one-line Python `replace()`. The gated run did the same task with
`atto_find` then one `atto_sub` carrying all three replacements. Both atto errors
came on that same task and were recovered inside it: all 18 gated runs landed.

Read it honestly: **the gate has not been shown to make a model more correct.**
Success was identical in this run, and the cost is real — five tool schemas against
two, repeated in every request. The win is that the operation the tool list could
not express now has a tool, so the model stays inside it. The correctness argument
needs a model that actually mis-edits, and this one did not.
