# atto-edit: the agent's editing contract

The first-class user is an agent. There is no window, no keymap, no plugin API. The product is a
small binary and a contract: **the document lives outside the agent's context, is addressed in a
coordinate system the agent can see, and changes only through guarded atomic transactions that
return proof.**

This is the whole product: one binary, one extension, one contract.

## Why the current primitive fails

An agent is a blind programmer with a context window. It can read text, emit text, and run a
command. It cannot count bytes, remember a file, or guarantee its own output is well formed.

Three flaws in the tools agents use today:

1. **The transport mangles code.** A hand-written JSON string inside a shell command is a lossy
   channel for text containing quotes and backslashes. Sessions reach for `python3 - <<'PY'`
   because a heredoc is the one transport that round-trips bytes.
2. **Read and write use different coordinate systems.** The agent reads windowed, numbered lines,
   then must reconstruct whole-file bytes to locate a change. One stale indent and the match fails.
3. **There is no proof.** "ok" or an error, then a re-read to find out what actually happened.

Evidence from this machine:

| Signal | Count |
|---|---|
| pi sessions containing a `python3 - <<` file surgery | 272 of 2,252 |
| pi sessions containing a failed exact-match edit | 232 of 2,252 |
| pi sessions containing the `edits.0: must be object` transport failure | 22 of 2,252 |
| this repo: Python heredocs vs `edit` calls | 428 vs 117 |

The 2026 tooling landscape (Codex `apply_patch`, Claude Code `str_replace`, hashline anchors,
snapshot merges, Cursor/Morph/Relace fast-apply models) agrees on two things even where it
disagrees on everything else: **do not make the model retype the old text**, and **do not
fuzzy-apply silently**. Benchmarks disagree on whether anchors or exact replace wins; they do not
disagree that retyping and silent fuzz are where precision dies. Fuzzy matching fired zero times
in 114 successful edits in one independent trace, while Codex's fuzzy seek has an open bug that
silently rewrites context-line indentation.

## The contract

1. **Address = (file, snapshot, line span).** A snapshot is an immutable content hash. Line spans
   are 1-based and inclusive, the coordinate system the agent already sees in every read.
2. **Payload = new text only, literal.** The agent never retypes the old text and never escapes
   the new text.
3. **Apply = atomic, snapshot-guarded, one transaction per call.** A stale or ambiguous request
   changes nothing.
4. **Report = diff + new snapshot + changed spans.** Verification is part of the mutation, not a
   second round trip.
5. **Fail = closed, with the current state of the contested region.** An agent's error message is
   its next prompt.

## The model

```
atto read FILE [FROM:TO]
```

Returns a snapshot id and numbered lines:

```
file path/to/file.rs
rev 8f3a1c2d9e0b
lines 335
41|    pub fn apply(&mut self, tx: Transaction, kind: Kind) -> Result<(), ChangeError> {
42|        let (forward, inverse) = self.buf.apply(tx)?;
...
```

The snapshot is stored content-addressed under `$ATTO_HOME/snapshots/`. The agent carries only
the id. The id is `sha256(absolute_path + "\0" + content)[:12]`, so a snapshot cannot be confused with
another file's.

```
atto find FILE TEXT
```

The same header, then only the lines containing `TEXT`:

```
file path/to/file.rs
rev 8f3a1c2d9e0b
lines 335
matches 2
41|    pub fn apply(&mut self, tx: Transaction, kind: Kind) -> Result<(), ChangeError> {
108|    pub fn apply(&mut self, batch: Batch) -> Result<(), ChangeError> {
```

Two matches and two numbers: either the text is unique enough to address, or the agent has learned
that it is not before writing anything. The match is literal and case-sensitive, because a selector
that guesses is worse than no selector. Counting lines by hand is where an agent gets the address
wrong, so the tool does the counting.

```
atto patch < plan
```

The plan names the file and the snapshot, then hunks. The tool loads the snapshot, applies the
hunks to it to get the intended post-image, and reconciles that with the live file:

- **live == snapshot**: apply at the line numbers.
- **live != snapshot**: for each hunk, locate the snapshot's old block (plus up to three lines of
  context) in the live file.
  - unique match: map the hunk to its new position and apply.
  - no match: conflict. Nothing is written.
  - several matches: conflict, with the candidate line numbers. Nothing is written.
- **never fuzzy-apply**. Whitespace is not normalized, text is not guessed, and the tool does not
  pick between two plausible places.

This is a three-way merge specialised to hunks the agent already declared. Changes made outside
the hunk (a formatter, another agent, a `cargo fmt`) are preserved. A change inside the hunk is a
conflict, because the agent's pre-image is no longer true.

## Plan grammar

Line-oriented and literal. No JSON, no escaping. One plan is one transaction.

```
# comments start with # outside a body
file path/to/file.rs
rev 8f3a1c2d9e0b

SWAP 41.=48:
+    pub fn apply(&mut self, tx: Transaction, kind: Kind) -> Result<(), ChangeError> {
+        let (forward, inverse) = self.buf.apply(tx)?;
+        self.history.record(forward, inverse, kind, Instant::now());
+        self.sels.map_through(&inverse);
+        Ok(())
+    }

DEL 100.=104

INS.POST 88:
+    // The guard is load bearing: ...
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

- Body lines start with `+`. The first `+` is stripped. A literal line starting with `+` is
  written `++`. An empty line is `+`.
- A body ends at the next directive, comment, or blank line. A non-directive line outside a body
  is an error, so a forgotten `+` is caught instead of silently inserted.
- Hunks must not overlap. `INS.PRE A` and `INS.POST A-1` are the same position and are rejected
  together.
- One `file` and one `rev` per plan. Multi-file plans are a v2 concern; the atomicity that
  matters first is within a file.

## CLI

```
atto read FILE [FROM:TO]         numbered lines + snapshot id
atto find FILE TEXT              matching lines + snapshot id (literal, case-sensitive)
atto stat FILE                   snapshot id, line count, byte count (records the snapshot)
atto patch [--allow-duplicate]   apply a plan from stdin
atto undo FILE                   revert the last transaction, if the file still matches
atto log [FILE]                  recent transactions
atto version
```

`read` caps output at 2,000 lines unless a range is given, and says so in the header. `find`
returns only the matching lines, so locating a change costs a fraction of a full read and the agent
never counts lines by hand. Data goes to stdout, diagnostics to stderr. Exit 0 on success, 1 on any
failure, 2 on usage.

## Guarantees

- **Atomic.** Temp file in the same directory, `fsync`, `rename`, directory `fsync`. Readers never
  observe a partial file.
- **Locked.** `flock` on the target for the read-check-write window, so two patches cannot both
  believe they are applying to the same pre-image.
- **Byte-faithful.** UTF-8 only; binary files are refused. BOM preserved. CRLF preserved as a
  file-level style. Trailing newline presence preserved. File mode preserved. Symlinks are
  resolved and the target is written, not the link.
- **No collateral damage.** Only the declared spans change. No trimming, reindenting, or
  reformatting.
- **Reversible.** Every transaction appends `{time, path, pre, post, plan}` to
  `$ATTO_HOME/journal.jsonl`. `undo` restores `pre` only when the file still hashes to `post`.
- **Observable.** The journal is the event stream a future live view consumes. It is also the
  audit log.

## What we deliberately do not build

- **No fuzzy matching.** Near-miss whitespace is not the real failure mode; hallucinated content
  is. A fuzzy match that succeeds is worse than a precise one that fails.
- **No merge model.** Deterministic first. A network round trip and a non-deterministic merge are
  the opposite of "precision".
- **No per-line anchors in v1.** The snapshot already gives the agent a stable address and makes
  stale state detectable; anchors add read-side token overhead and bookkeeping. If dogfooding
  shows a need for chain-without-read, anchors are a v2 layer on the same model.
- **No AST or symbol addressing.** Line spans cover almost every edit. Symbol resolution is a
  later layer that maps names to spans; it must not leak into the core.
- **No daemon, GUI, MCP server, or plugin API in v1.** The core is a library with a CLI. A thin
  stdio MCP adapter and a pi extension are transports over the same core, added after the
  contract survives dogfooding.

## Benchmarks

Two harnesses, both reproducible.

`bench/bench.py` is the quick one: five edits, exact replace vs Python vs atto. It isolates the
failure modes (the typo that silently mangles indentation, the duplicate that exact replace
refuses and Python first-match corrupts).

`bench/tools_bench.py` is the real one: the same tasks through the actual tools — exact replace
(pi/Claude-Code semantics), the Python `s.replace` workaround, `s.replace` without a uniqueness
check, hashline 0.9.26, snapedit 1.0.0, and atto. Twelve tasks across Rust, Go, and Python,
including typo, drift, duplicate, stale, and a 5,000-line file. A failure is retried after a
re-read, the way an agent recovers; attempts and bytes include every retry. The stale task is a
safe-failure test: the file changes inside the target after the read, and the only correct outcome
is a refusal with zero writes.

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

What the numbers say:

- **Atto lands the most with the fewest retries.** 11/11 and 1/1 in 12 attempts; exact, Python,
  and hashline need 15; snapedit misses the duplicate-line task entirely.
- **atto-stat sends 2.2x fewer total bytes than exact, 2.6x fewer than Python, 105x fewer than
  hashline, and 85x fewer than snapedit. atto read+patch is 1.2x, 1.4x, 59x, and 48x.** The gap
  is the read side: hashline reads every line with a hash, snapedit ships a JSON page, atto reads
  a line range plus a 12-character rev. On the 5,000-line task hashline reads 153,917 B; atto
  reads 284 B.
- **Hashline is fastest, and that is the honest trade.** It defaults to a non-atomic direct write;
  atto fsyncs and renames by default. Hashline also reads the whole file and needs two attempts on
  every drift; atto relocates in one.
- **Snapedit is safe but content-anchored.** Its 3-way merge refuses a duplicated hunk even when
  the snapshot line number is correct, so the duplicate task fails after three attempts. Atto uses
  the line number as the address and the context as the guard.
- **Exact replace and Python are only competitive on bytes because they run in-process and read
  just the target block.** They still need 15 attempts, and on the typo task they silently rewrite
  indentation; the retry is what saves them.

Latency: atto's stat+patch is two process spawns, ~24 ms per task here; hashline is ~18 ms; exact
is in-process. A daemon would close the remaining gap, but 6 ms does not justify one.


## Build and try

```sh
go build -o atto ./cmd/atto
./atto read bench/fixtures/sample.rs 1:6
./atto patch <<'ATTO'
file bench/fixtures/sample.rs
rev <the rev from read>
SWAP 3.=3:
+        return 1;
ATTO
./atto undo bench/fixtures/sample.rs
```


## Open questions

1. **Read cap.** 2,000 lines is a guess. The right number is the largest window that still leaves
   room for a patch and a verification diff in a mid-size context.
2. **Multi-file transactions.** Needed for refactors; the atomicity story is a journal-backed
   two-phase write, not a single `rename`.
3. **Anchors.** If chaining edits without a re-read matters, add a short per-line hash to `read`
   and accept it in the plan.
4. **Undo scope.** Global journal vs per-repo vs per-session. Global is simple; per-session is
   safer when several agents edit one tree.
