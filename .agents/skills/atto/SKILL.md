---
name: atto
description: Edit an existing file with atto — snapshot-guarded, line-addressed, atomic. Use before changing any existing file, when an edit conflicts or a rev is stale, or when you need numbered lines plus a rev for a precise change.
---

# atto

One mutation path for existing files: read the lines, hold the rev, send only the new text.

## The loop

1. `atto_read(path, from?, to?)` — numbered lines and a 12-character rev. Read the region you
   will change, not the whole file.
2. `atto_edit(path, rev, changes)` — `{start, end, text}` hunks, all in one atomic transaction,
   one undo step.
3. The response is a diff, a new rev, and the new line spans.

Creating a new file is still `write`. Everything else about an existing file goes through atto.

## Rules that are not negotiable

- No `edit`, no `write` on an existing file, no Python, `sed`, `perl`, `tee`, `git apply`, or
  redirect to change a file. The shell guard blocks these. A block is not a hint to find a
  different route to the same write — use `atto_edit`.
- Every hunk in one call is numbered against the rev you passed. Do not renumber between hunks;
  put them all in one call.
- After the call, the line numbers you are holding are stale. Chain the next edit with the
  returned rev and the returned spans, or `atto_read` again. When two edits touch one file, this
  is where the mistake happens: the second call must not reuse the first call's numbering.
- `end = start - 1` inserts before `start`. Empty `text` deletes `start..end`.
- One call per file per round. Independent changes to a file belong in the same call.

## A conflict is information

A stale rev writes nothing and returns the contested lines. Re-read, look at what changed, and
retry with the new rev. Retrying the same rev will fail the same way.

## CLI, when there is no pi tool

```sh
atto read FILE 41:48     # numbered lines + rev
atto stat FILE           # just the rev, ~120 bytes
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
`cd /tmp`. `=>`, `>=`, and a `>` inside quotes are not redirects. Read-only shell — `cat`, `grep`,
`git diff`, `python3 -c "print(open(f).read())"` — is untouched.

`ATTO_ALLOW_SHELL_EDITS=1` disables the guard. It exists for migrations and one-off scripts, not
for agent work.

## Is it actually being used

```
/atto-stats           # reads, edits, undos, blocked, failed, log path
```

Log: `$ATTO_HOME/pi.jsonl` (default `~/.local/state/atto/pi.jsonl`). Target state after real tasks
is `blocked: 0` and `failed edits: 0`.
