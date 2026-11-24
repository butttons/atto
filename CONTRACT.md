# atto: the editing contract

The first-class user is an agent. There is no window, no keymap, no daemon. The
product is a small binary and a contract: **the document lives outside the
agent's context, is addressed in a coordinate system the agent can see, and
changes only through guarded atomic transactions that return proof.**

## The contract

1. **Address = (file, rev, span) or (file, rev, exact text).** A rev is an
   immutable content hash. Spans are 1-based and inclusive, the coordinate
   system every read already prints; exact text is what most edits actually mean.
2. **Payload = new text only, literal.** The agent never retypes the old text and
   never escapes the new text.
3. **Apply = atomic, rev-guarded, one transaction per call.** A stale, absent, or
   ambiguous request changes nothing.
4. **Report = diff + new rev + changed spans.** Verification is part of the
   mutation, not a second round trip.
5. **Fail = closed, with the current state of the contested region.** An agent's
   error message is its next prompt.

## read and find

```
atto read FILE [FROM:TO]
```

```
file path/to/file.rs
rev 8f3a1c2d9e0b
lines 335
41|    pub fn apply(&mut self, tx: Transaction, kind: Kind) -> Result<(), ChangeError> {
42|        let (forward, inverse) = self.buf.apply(tx)?;
```

The snapshot is stored content-addressed under `$ATTO_HOME/snapshots/`, and the
rev is `sha256(absolute_path + "\0" + content)[:12]`, so a rev cannot be replayed
against another file. A whole-file read stops at 2,000 lines and says so:
`showing 1..2000`, `truncated 2000/5000`.

```
atto find FILE TEXT
```

The same header, then only the lines containing `TEXT`, up to 200 of them:

```
file path/to/file.rs
rev 8f3a1c2d9e0b
lines 335
matches 2
41|    pub fn apply(&mut self, tx: Transaction, kind: Kind) -> Result<(), ChangeError> {
108|    pub fn apply(&mut self, batch: Batch) -> Result<(), ChangeError> {
```

The match is literal and case-sensitive, because a selector that guesses is worse
than no selector. Counting lines by hand is where an agent gets the address wrong,
so the tool does the counting.

```
atto stat FILE
```

Rev, line count, byte count, no lines. It records the snapshot, so `stat` plus an
edit costs one line-range read instead of a whole file.

## sub: exact text

Measured over two months of real sessions, most edits that fell back to Python
were a literal `replace()`. A tool that speaks only spans leaves the agent no way
to say the one thing it means, so it writes a script. `sub` is addressed by
content, and the rev is still the guard:

```
atto sub path/to/conf.ini --rev 8f3a1c2d9e0b
-retries=3
+retries=5
@@
-render()
+render(opts)
```

- `-` lines are the text to find, the `+` lines are what replaces them. A block
  with no `+` lines is a deletion.
- `@@` separates blocks. It is optional: a `-` line after a `+` line starts a new
  block.
- Matching is literal and contiguous, byte for byte, including indentation. A near
  miss is a refusal, not a best guess.
- Blank lines in the input are layout. To match a blank line, write `-` alone.
- The count is checked. One match is the default; several are refused unless
  `--expect N` states the count or `--all` accepts any. Zero matches is always a
  refusal.
- `--dry` renders the diff and the prospective rev without writing.
- All blocks in one call are one transaction: one rev in, one rev out, one journal
  entry.

The count is the point. `replace()` silently does nothing when the text is absent,
and silently replaces every copy when it is present several times. Both are
refusals here, and the refusal names every matched line so the next call can be
exact.

## patch: line spans

The plan names the file and the rev, then hunks. The tool loads the snapshot,
applies the hunks to it to get the intended post-image, and reconciles that with
the live file:

- **live == snapshot**: apply at the line numbers.
- **live != snapshot**: for each hunk, locate the snapshot's old block (plus up to
  three lines of context) in the live file.
  - unique match: map the hunk to its new position and apply.
  - no match: conflict. Nothing is written.
  - several matches: conflict, with the candidate line numbers. Nothing is written.
- **never fuzzy-apply.** Whitespace is not normalized, text is not guessed, and the
  tool does not pick between two plausible places.

This is a three-way merge specialised to hunks the agent already declared. Changes
outside the hunk (a formatter, another agent) are preserved. A change inside the
hunk is a conflict, because the agent's pre-image is no longer true.

## Plan grammar

Line-oriented and literal. No JSON, no escaping. One plan is one transaction.

```
# comments start with # outside a body
file path/to/file.rs
rev 8f3a1c2d9e0b

SWAP 41.=48:
+    pub fn apply(&mut self, tx: Transaction, kind: Kind) -> Result<(), ChangeError> {
+        Ok(())

DEL 100.=104

INS.POST 88:
+    // The guard is load bearing.
```

| Directive | Meaning |
|---|---|
| `SWAP A.=B:` | replace snapshot lines A through B, inclusive, with the following body |
| `DEL A.=B` | delete snapshot lines A through B, inclusive |
| `INS.PRE A:` | insert the body before line A |
| `INS.POST A:` | insert the body after line A |
| `INS.HEAD:` | insert the body before line 1 |
| `INS.TAIL:` | insert the body after the last line |

Rules:

- Body lines start with `+`. The first `+` is stripped. A literal line starting
  with `+` is written `++`. An empty line is `+`.
- A body ends at the next directive, comment, or blank line. A non-directive line
  outside a body is an error, so a forgotten `+` is caught instead of inserted.
- Hunks must not overlap. `INS.PRE A` and `INS.POST A-1` are the same position and
  are rejected together.
- One `file` and one `rev` per plan. Multi-file plans are a later concern; the
  atomicity that matters first is within a file.

## CLI

```
atto read FILE [FROM:TO]         numbered lines + rev
atto find FILE TEXT              matching lines + rev (literal, case-sensitive)
atto stat FILE                   rev, line count, byte count
atto patch [--allow-duplicate]   apply a line-addressed plan from stdin
atto sub FILE --rev R            replace exact text: '-'/'+' blocks on stdin
                                 [--all | --expect N] [--dry]
atto undo FILE                   revert the last transaction, if the file still matches
atto log [FILE]                  the last 20 transactions
atto version
```

Data goes to stdout, diagnostics to stderr. Exit 0 on success, 1 on any failure,
2 on usage.

## Guarantees

- **Atomic.** Temp file in the same directory, `fsync`, `rename`, directory
  `fsync`. Readers never observe a partial file.
- **Locked.** `flock` on the target for the read-check-write window, so two patches
  cannot both believe they are applying to the same pre-image.
- **Byte-faithful.** UTF-8 only; a binary file is refused. BOM preserved. CRLF
  preserved as a file-level style. Trailing newline presence preserved. File mode
  preserved. Symlinks are resolved and the target is written, not the link.
- **No collateral damage.** Only the declared spans change. No trimming,
  reindenting, or reformatting.
- **Reversible.** Every transaction appends `{time, path, pre, post, plan}` to
  `$ATTO_HOME/journal.jsonl`. `undo` restores `pre` only when the file still
  hashes to `post`.
- **Observable.** The journal is the audit log, and what `atto log` prints.

## Not built, on purpose

- **No fuzzy matching.** A near-miss whitespace is not the failure mode;
  hallucinated content is. A fuzzy match that succeeds is worse than a precise one
  that fails.
- **No model in the loop.** Deterministic. A network round trip and a
  non-deterministic merge are the opposite of precision.
- **No per-line anchors.** The rev gives the agent a stable address and makes stale
  state detectable; anchors add read-side overhead for a problem the rev already
  covers.
- **No AST or symbol addressing.** Spans and exact text cover the edits; symbol
  resolution is a layer that maps names to spans and must not leak into the core.
- **No daemon, GUI, or MCP server.** The core is a library with a CLI. The pi
  extension is a transport over that CLI, not a second implementation.

## Benchmarks

Two harnesses, both reproducible.

`bench/bench.py` is the quick one: five edits, exact replace vs Python vs atto. It
isolates the failure modes — the typo that silently mangles indentation, the
duplicate that exact replace refuses and Python first-match corrupts.

`bench/tools_bench.py` drives the real CLIs: exact replace (pi/Claude-Code
semantics), the Python `s.replace` workaround, `s.replace` without a uniqueness
check, hashline 0.9.26, snapedit 1.0.0, and atto. Twelve tasks across Rust, Go, and
Python, including typo, drift, duplicate, stale, and a 5,000-line file. A failure
is retried after a re-read, the way an agent recovers; attempts and bytes include
every retry. The stale task is a safe-failure test: the file changes inside the
target after the read, and the only correct outcome is a refusal with zero writes.

Results on this machine (2026-11-24):

| tool | landed | safe | attempts | read B | edit B | total B | ms |
|---|---|---|---|---|---|---|---|
| exact (pi/Claude) | 11/11 | 1/1 | 15 | 3,162 | 2,965 | 6,127 | 0 (in-process) |
| python `s.replace` | 11/11 | 1/1 | 15 | 3,162 | 4,134 | 7,296 | 486 |
| python first-match | 11/11 | 1/1 | 15 | 3,162 | 3,759 | 6,921 | 454 |
| hashline 0.9.26 | 11/11 | 1/1 | 15 | 296,556 | 1,287 | 297,843 | 219 |
| snapedit 1.0.0 | 10/11 | 1/1 | 14 | 239,422 | 2,887 | 242,309 | 1,229 |
| atto (read+patch) | 11/11 | 1/1 | 12 | 3,703 | 1,357 | 5,060 | 278 |
| atto (stat+patch) | 11/11 | 1/1 | 12 | 1,479 | 1,357 | 2,836 | 265 |

The table predates `atto sub`, so atto's column understates what the binary now
is: `sub` replaces the read-side cost on the tasks it covers.

- **Atto lands the most with the fewest retries.** 11/11 and 1/1 in 12 attempts;
  exact, Python, and hashline need 15; snapedit misses the duplicate-line task.
- **atto-stat sends 2.2x fewer total bytes than exact, 2.6x fewer than Python, 105x
  fewer than hashline, 85x fewer than snapedit.** The gap is the read side:
  hashline reads every line with a hash, snapedit ships a JSON page, atto reads a
  line range plus a 12-character rev. On the 5,000-line task hashline reads
  153,917 B and atto reads 284 B.
- **Hashline is fastest, and that is the trade.** It defaults to a non-atomic
  direct write; atto fsyncs and renames. It also reads the whole file and needs two
  attempts on every drift; atto relocates in one.
- **Snapedit is safe but content-anchored.** Its 3-way merge refuses a duplicated
  hunk even when the snapshot line number is correct, so the duplicate task fails
  after three attempts. Atto uses the line number as the address and the context as
  the guard.
