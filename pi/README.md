# atto for pi

Make pi edit through atto, and make the fallback visible.

The extension is a gate, not a request:

- registers `atto_read`, `atto_find`, `atto_sub`, `atto_edit`, `atto_undo`
- removes the built-in `edit` and `apply_patch` from the model's tool set
- blocks `edit`/`apply_patch` if a codemode script or a resumed session reaches them
- blocks `write` when the target already exists (new files are still fine)
- blocks shell commands that can only mean "edit a file": `sed -i`, `perl -pi`, `tee`, `git apply`,
  `apply_patch`, `hashline patch`, and redirects into anything except `/tmp`, `target/`, and
  `/dev/*`
- does not mistake code for a redirect: `=>`, `>=` and `>` inside quotes are left alone, and a
  redirect after `cd /tmp` — including inside a subshell or on a later line — is scratch
- refuses an edit to lines this session never showed the model, so a rev cannot authorize changes
  to a region the model only remembers
- logs every atto edit, failure, refusal, and blocked fallback to `$ATTO_HOME/pi.jsonl`, so "is the
  agent actually using it?" is a number, not a hope

The guard classifies commands and operators, never a program's text. An earlier version tried to
read `python3 -c` and heredocs to decide whether the code could write; it produced more false
positives than catches, and its bypass was one line long. See "What this does not stop" below.

## Build and load

```sh
go build -o atto ./cmd/atto

# one invocation
ATTO_BIN="$PWD/atto" pi --extension "$PWD/pi/atto.ts" \
  --tools read,bash,grep,find,ls,atto_read,atto_find,atto_sub,atto_edit,atto_undo

# or make it the default: symlink it into the personal extensions directory
ln -s "$PWD/pi/atto.ts" ~/.pi/agent/extensions/atto.ts
```

`--tools` with plain names replaces the default set, so list the read-only tools you still want.
If you use `settings.json`, add the extension path and put the same allowlist in `defaultTools`:

```jsonc
{
  "extensions": ["/absolute/path/to/atto/pi/atto.ts"],
  "defaultTools": ["read", "bash", "grep", "find", "ls", "atto_read", "atto_find", "atto_sub", "atto_edit", "atto_undo"]
}
```

If the binary is not on `PATH`, set `ATTO_BIN` in the environment that starts pi. The extension also
removes `edit`/`apply_patch` from the active set on `session_start`, so it still holds if the tool
list is not pinned.

## Install as a pi package

```sh
pi install git:github.com/butttons/atto
pi update git:github.com/butttons/atto     # after a change lands
```

Installing clones the extension into `~/.pi/agent/git/github.com/butttons/atto`, and `pi update` is
the only thing that refreshes that clone. Editing a local checkout does not affect an installed copy.
Loading both at once is a hard error (`Tool "atto_read" conflicts with ...`), so pass `--extension`
only on a machine where the package is not installed.

## The loop the model is taught

1. `atto_find(path, text)` or `atto_read(path, from, to)` — the numbered lines, plus a rev.
2. `atto_sub(path, rev, changes)` — `{old, new}` text replacement. This is the default verb: most
   edits are "replace this text with that text", and the model reaches for a Python script when the
   tool list cannot say it. `expect` / `all` state how many matches are intended.
3. `atto_edit(path, rev, changes)` — `{start, end, text}` hunks, for a change that is genuinely a
   line range (moving a block, deleting lines, an insertion by position).
4. The result is a diff, a new rev, and the new line spans.

`end = start - 1` inserts before `start`. Empty `text` deletes `start..end`. A conflict writes
nothing and returns the contested lines; the model re-reads and retries.

## What a rev authorizes

A rev proves the file has not moved. It does not prove the model looked at the lines it is
changing, and a model can write a perfectly valid patch for the wrong place: addresses held in
memory drift after the first edit, and a one-line read still returns a whole-file rev.

So the extension remembers what it has shown, per path and rev, and refuses anything else:

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

A refusal writes nothing and says which lines were shown, so recovering costs one `atto_read` or
`atto_find` rather than a guess. This is per-session on purpose: a fresh session has shown nothing,
so it reads before it edits.

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

The target is `blocked: 0`, `refused: 0` and `failed edits: 0` after a few real tasks. A blocked
count is the shell guard catching a fallback the model tried; a refusal is the window gate catching
an edit to lines it never read. Both are the tool working, and both name their reason in the log.
Repeated refusals mean the prompt guidance is not landing, not that the gate is wrong.

## Guard tests

Both guards are pure modules, tested directly instead of by driving a model at them:

```sh
npm test          # guard.test.ts and windows.test.ts, 48 cases, no model involved
```

Every shell-guard case is a false positive or a bypass that actually happened: a scratch redirect
after `cd /tmp`, `2>&1`, `=>`, `>=`, a quoted `>` and `/dev/null` must pass; a python heredoc, a
writing `python3 -c`, `sed -i`, `tee`, and a write anywhere outside scratch must not.

`windows.ts` covers the addressing gate: a span inside one region, a span spanning a gap, a
one-line read that does not authorize a block, an insertion at the edge of the shown range, a stale
rev, and reading `truncated 2000/5000` as `1..2000` rather than the range that was asked for.

## What this does not stop

The gate is not a sandbox. It removes the tools and the habitual routes, and it refuses edits to
lines the model never read. It does not stop a model that decides to write a file from inside a
program:

```sh
python3 -c "open('main.go','w').write('...')"   # allowed
python3 - <<'PY'                                 # allowed
open('main.go','w').write('...')
PY
```

That is a deliberate trade. Classifying a program's behaviour with a regex cost six false
positives in one session — a heredoc used to *read* JSON, a read of a `.github/workflows/` path, a
`(cd dist && ...)` subshell — and the bypass was always one line away. The remaining rules fire on
commands that have no other meaning, so they do not misfire on ordinary work.

What is left is the honest claim: atto is the path of least resistance, and `atto_read`/`atto_edit`
are the only tools that can change an existing file. A model that goes around it is visible in the
log rather than impossible.

## Escape hatch

`ATTO_ALLOW_SHELL_EDITS=1` disables the shell guard. Do not use it for normal agent work; it
exists so a migration or a one-off script can get through without unloading the extension.

## What the A/B actually measured

`bench/ab.py` runs the same tasks through the same model twice: stock pi (`read,bash,edit,write`)
and gated pi (`read,bash,atto_read,atto_find,atto_sub,atto_edit,atto_undo`). Success is the file
matching the expected bytes. Every run gets a fresh directory, a fresh session, no context files,
skills or MCP, and a throwaway config directory, so the only difference is the editing tools.

```sh
python3 bench/ab.py --runs 3 --provider opencode-go --model glm-5.3-flash
```

20 tasks x 3 runs x 2 conditions. Two tasks were bad fixtures and are excluded (one rewarded
running `gofmt` via bash rather than editing precisely; one had ambiguous backslash escaping). On
the remaining 18, all 54 runs per condition landed:

| | stock | atto |
|---|---|---|
| tasks landed | 54/54 | 54/54 |
| tool calls, total | 145 | 138 |
| input tokens | 322,727 | 392,504 (+22%) |
| output tokens | 6,782 | 6,942 |
| mean wall time | 7.0 s | 7.6 s |
| shell fallbacks | 2 | 0 |
| tool errors | 0 | 9 |

Read this honestly: **on simple single-file edits the gate did not make the model more successful.**
Success was identical. It cost 22% more input tokens and 9% more wall time.

Where the tokens went: 352 of the per-request tokens are prompt overhead (four tool schemas and
their guidelines against two), about 82% of the delta. The rest is atto's output: numbered lines
with a rev, and a diff on every edit.

What the gate did buy, in the same runs: no shell fallbacks, slightly fewer tool calls, and every
edit carrying a rev and a diff. The 9 tool errors were all recoverable refusals — a model edits a
range it has not read, is told so, reads, and retries; none of them lost a task.

Single-line edits on a small file are the easy path for exact-match editing too, so the second run
went after the shapes atto exists for: the third of three identical lines, a 130-character line to
retype in full, numbering that shifts after an insert, a file that changes under the model between
its read and its edit, a whitespace-only edit, and a deletion whose neighbours must survive. 6
tasks x 3 runs x 2 conditions:

| | stock | atto |
|---|---|---|
| tasks landed | 18/18 | 18/18 |
| tool calls, total | 61 | 49 |
| input tokens | 130,129 | 124,360 |
| mean wall time | 9.0 s | 8.2 s |
| tool errors | 2 | 0 |

The direction flips here: atto took 20% fewer calls, 4.5% fewer input tokens, and no retries. Stock
spends those calls reconstructing exact old text to match on — which is the work atto's addressing
removes. Success is still identical, so this is an efficiency result, not a correctness one.
A third run went after the specific failure the tool list had: the operations that made an agent
write a Python script instead of using a tool. Replace every copy of a line, three scattered changes
in one pass, a two-line block swapped for three lines, the third of three identical lines, a typo in
one of two similar words, and an exact block deletion. 6 x 3 x 2, in `bench/ab-results-sub.json`:

| | stock | atto |
|---|---|---|
| tasks landed | 18/18 | 18/18 |
| tool calls, total | 47 | 43 |
| input tokens | 106,867 | 132,212 |
| shell fallbacks | 1 | 0 |
| tool errors | 0 | 2 |

The stock run that fell back to the shell did it on *replace every occurrence*: `read` then `bash`, a
one-line Python `replace()`. The gated run did the same task with `atto_find` then one `atto_sub`
carrying all three replacements. That is the point of the verb — the operation the tool list could
not express now has a tool, and the model reaches for it.

The token cost is still real and still prompt-shaped: five tool schemas against two. It is the part
of this design that has to keep being paid down.

The honest summary: **the gate did not make this model more correct on any task I gave it.** It made
it cheaper on the tasks where exact-match editing is expensive, and slightly dearer on the trivial
ones before the prompt was trimmed. The correctness argument for atto remains theoretical: it needs
a model that actually mis-edits, and `glm-5.3-flash` did not.
