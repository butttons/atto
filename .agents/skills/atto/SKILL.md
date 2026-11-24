---
name: atto
description: Edit an existing file with atto — snapshot-guarded, line-addressed, atomic. Use before changing any existing file, when an edit conflicts or a rev is stale, or when you need numbered lines plus a rev for a precise change.
---

# atto

One mutation path for existing files: read the lines, hold the rev, send only the new text.

## The loop

1. `atto_find(path, text)` or `atto_read(path, from?, to?)` — numbered lines and a 12-character
   rev. Find when you know the text; read when you know the region.
2. `atto_sub(path, rev, changes)` — `{old, new}` text replacement. This is the default verb: use it
   whenever you are changing text that already exists and you can quote it exactly.
3. `atto_edit(path, rev, changes)` — `{start, end, text}` hunks for a change that is genuinely a
   line range: moving a block, deleting by position, inserting at a line.
4. The response is a diff, a new rev, and the new line spans.

Creating a new file is still `write`. Everything else about an existing file goes through atto.

## Choosing the verb

| You are | Use |
|---|---|
| changing text that already exists and you can quote it | `atto_sub` |
| replacing every copy of a line | `atto_sub` with `all: true` — one call, and it reports the count |
| making several changes in one pass | one `atto_sub` with several `{old, new}` changes |
| moving, deleting or inserting by line number | `atto_edit` |
| rewriting most of a file | `atto_edit` over the whole span, after reading it |

`atto_sub` is the common case, not the specialist case. If you catch yourself reaching for Python
to `open()` a file, join lines, or `replace()`, that call is `atto_sub` — and it will refuse to do
the wrong thing where a script would do it silently.

```
atto_sub(path, rev, changes: [{ old: "retries = 3", new: "retries = 5" }])
atto_sub(path, rev, changes: [{ old: "TODO", new: "- [ ] task" }], all: true)
atto_sub(path, rev, changes: [{ old: "a", new: "A" }, { old: "b", new: "B" }])
```

The `old` text must match byte for byte, including indentation. If it matches nowhere, the call is
refused. If it matches more than once and you did not say how many, the call is refused and lists
the line numbers — add surrounding lines to pin one match, or pass `all: true`, or `expect: N`.

## You may only change lines you were shown

The rev is bound to the lines the tool returned. Reading 40..50 authorizes 40..50, not the file.
Find authorizes the lines it matched. An edit authorizes the spans it just wrote. Anything else is
refused with the ranges that were shown, and nothing is written.

This is not a formality. A model that edits from remembered line numbers is one edit away from
writing a valid patch into the wrong place, and the diff will look plausible.

## Rules that are not negotiable

- No `edit`, no `write` on an existing file, no Python, `sed`, `perl`, `tee`, `git apply`, or
  redirect to change a file. The shell guard blocks these. A block is not a hint to find a
  different route to the same write — use `atto_sub` or `atto_edit`.
- That applies to scripts too. A `python3 - <<'PY'` that reads, transforms and writes is the exact
  habit `atto_sub` exists to end: it is the same edit, but with no rev, no count check, no atomic
  write, and nothing to undo.
- Every hunk in one call is numbered against the rev you passed. Do not renumber between hunks;
  put them all in one call.
- After the call, the line numbers you are holding are stale. Re-read before the next edit unless
  it targets the spans the last one returned.
- `end = start - 1` inserts before `start`. Empty `text` deletes `start..end`.
- Do not put an existing neighbouring line into `text`. If the replacement repeats text that already
  sits next to the span, atto refuses it: that is what duplicating a block looks like.

## Read the notes

The result can end with `note: lines 3 and 4 are now identical`. That is the signature of a block
pasted twice. Fix it in the next call; do not explain it away.

## A conflict or a refusal is information

A stale rev writes nothing and returns the contested lines. A refusal names the range that had not
been shown. Both are fixed by one `atto_read` or `atto_find` and a retry with the rev it returns.
Retrying the same rev fails the same way.

## CLI, when there is no pi tool

```sh
atto read FILE             # whole file, numbered, + rev
atto read FILE 41:48       # just a region
atto find FILE 'text'      # matching lines + rev, literal
atto sub FILE --rev 8f3a1c2d9e0b <<'ATTO'
-old line
+new line
ATTO
atto sub FILE --rev R --all      # every match, count reported
atto sub FILE --rev R --dry      # diff only, writes nothing
atto patch <<'ATTO'
file path/to/file
rev 8f3a1c2d9e0b
SWAP 41.=48:
+new line 41
+new line 42
ATTO
atto undo FILE           # revert the last transaction, if the file still matches
atto log FILE            # recent transactions
```

Plan body lines start with `+`; a literal leading `+` is `++`. `SWAP A.=B` replaces A..B
inclusive, `DEL C.=D` deletes, `INS.PRE A` / `INS.POST A` insert, `INS.TAIL` appends. One file and
one rev per plan, hunks must not overlap. Full grammar: `CONTRACT.md`.

## What the guard allows

Scratch output is fine: redirects into `/tmp`, `target/`, and `/dev/*`, including after a
`cd /tmp` on any line or in a subshell. `=>`, `>=`, and a `>` inside quotes are not redirects.
Read-only shell — `cat`, `grep`, `git diff`, `python3 -c ...` — is untouched.

It blocks `sed -i`, `perl -pi`, `tee`, `git apply`, and redirects at tracked files. It does not
inspect program text, so it will not stop a script that writes. Do not be that script: the whole
value is the rev, and a python write has none.

`ATTO_ALLOW_SHELL_EDITS=1` disables the guard. It exists for migrations and one-off scripts, not
for agent work.

## Is it actually being used

```
/atto-stats           # reads, edits, undos, blocked, failed, refused, log path
```

Log: `$ATTO_HOME/pi.jsonl` (default `~/.local/state/atto/pi.jsonl`). Target state after real tasks
is `blocked: 0`, `refused: 0`, `failed edits: 0`. A refusal is the gate catching an edit to lines
you never read — fix the habit, not the number.
