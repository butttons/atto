# atto for pi

Make pi edit through atto, and make the fallback visible.

The extension is a gate, not a request:

- registers `atto_read`, `atto_find`, `atto_edit`, `atto_undo`
- removes the built-in `edit` and `apply_patch` from the model's tool set
- blocks `edit`/`apply_patch` if a codemode script or a resumed session reaches them
- blocks `write` when the target already exists (new files are still fine)
- blocks shell commands that edit files: `python3 - <<`, `python3 -c` that can write, `sed -i`,
  `perl -pi`, `tee`, `git apply`, `apply_patch`, `hashline patch`, and redirects into anything
  except `/tmp`, `target/`, and `/dev/*`
- does not mistake code for a redirect: `=>`, `>=` and `>` inside quotes are left alone, and a
  redirect after `cd /tmp` is recognised as scratch
- refuses an edit to lines this session never showed the model, so a rev cannot authorize changes
  to a region the model only remembers
- logs every atto edit, failure, refusal, and blocked fallback to `$ATTO_HOME/pi.jsonl`, so "is the
  agent actually using it?" is a number, not a hope

## Build and load

```sh
go build -o atto ./cmd/atto

# one invocation
ATTO_BIN="$PWD/atto" pi --extension "$PWD/pi/atto.ts" \
  --tools read,bash,grep,find,ls,atto_read,atto_find,atto_edit,atto_undo

# or make it the default: symlink it into the personal extensions directory
ln -s "$PWD/pi/atto.ts" ~/.pi/agent/extensions/atto.ts
```

`--tools` with plain names replaces the default set, so list the read-only tools you still want.
If you use `settings.json`, add the extension path and put the same allowlist in `defaultTools`:

```jsonc
{
  "extensions": ["/absolute/path/to/atto/pi/atto.ts"],
  "defaultTools": ["read", "bash", "grep", "find", "ls", "atto_read", "atto_find", "atto_edit", "atto_undo"]
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
2. `atto_edit(path, rev, changes)` — `{start, end, text}` hunks, all in one transaction.
3. The result is a diff, a new rev, and the new line spans.

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
npm test          # guard.test.ts and windows.test.ts, 50 cases, no model involved
```

Every shell-guard case is a false positive or a bypass that actually happened: a scratch redirect
after `cd /tmp`, `2>&1`, `=>`, `>=`, a quoted `>` and `/dev/null` must pass; a python heredoc, a
writing `python3 -c`, `sed -i`, `tee`, and a write anywhere outside scratch must not.

`windows.ts` covers the addressing gate: a span inside one region, a span spanning a gap, a
one-line read that does not authorize a block, an insertion at the edge of the shown range, a stale
rev, and reading `truncated 2000/5000` as `1..2000` rather than the range that was asked for.

## Escape hatch

`ATTO_ALLOW_SHELL_EDITS=1` disables the shell guard. Do not use it for normal agent work; it
exists so a migration or a one-off script can get through without unloading the extension.

## The proof that it is better

The deterministic harness is `../bench/tools_bench.py` (see [CONTRACT.md](../CONTRACT.md)). It compares the
substrate, not the model. The live proof is an A/B run of the same tasks with the same model:

```sh
# A: stock pi
pi --print --mode json --no-session --tools read,bash,edit,write \
  "In work/f.rs, change line 2 to BRAVO. Use the edit tool."

# B: atto enforced
ATTO_BIN="$PWD/atto" pi --print --mode json --no-session --extension "$PWD/pi/atto.ts" \
  --tools read,bash,atto_read,atto_find,atto_edit,atto_undo \
  "In work/f.rs, change line 2 to BRAVO."
```

Run 20 mutation tasks, 3 times each, and compare: task success, tool calls, retries, input/output
tokens, wall time, and blocked fallbacks. Success is the file matching the expected content (or the
tests passing), not the model saying it is done. That run is the next deliverable; the extension
records the data it needs.
