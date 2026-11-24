# atto

The agent-first editing binary. The contract is in [CONTRACT.md](CONTRACT.md).

## Build

```sh
cd tools/atto
go build -o atto .
```

No dependencies outside the standard library. Go 1.22+.

## Use

```sh
# 1. Read the region you are about to change. Keep the rev.
atto read bench/fixtures/sample.rs 1:6

# 2. Send only the new text, addressed to the rev's line span.
atto patch <<'ATTO'
file bench/fixtures/sample.rs
rev 8f3a1c2d9e0b
SWAP 3.=3:
+        return 1;
ATTO

# 3. The response is the proof: a diff, the new rev, and the new span.
# 4. If the file moved on, the patch refuses and shows the contested lines.
atto undo bench/fixtures/sample.rs
atto log bench/fixtures/sample.rs
```

If your agent already read the lines through another tool, `atto stat FILE` returns just the rev
(and records the snapshot) for about 120 bytes, so the plan is the only payload.

## Plan grammar

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

Body lines start with `+`; a literal leading `+` is `++`. `SWAP A.=B` replaces lines A through B
inclusive; `INS.PRE A` inserts before A; `INS.POST A` inserts after A. One file and one rev per
plan. Hunks must not overlap. See [CONTRACT.md](CONTRACT.md) for the full rules.

## Tests

```sh
go test ./...
go vet ./...
```

## Benchmark

```sh
go build -o atto .
cd ../..
python3 tools/atto/bench/bench.py --atto tools/atto/atto
python3 tools/atto/bench/tools_bench.py \
  --atto "$PWD/tools/atto/atto" \
  --hashline /path/to/hashline \
  --snapedit-cli /path/to/snapedit/src/cli.js
```

`bench.py` is the quick five-edit comparison. `tools_bench.py` drives the real `hashline` and
`snapedit` CLIs as well as exact replace and the Python workaround: 12 tasks including typo, drift,
duplicate, stale, and a 5,000-line file, with retries counted. Build the external tools first:

```sh
git clone https://github.com/quangdang46/hashline && (cd hashline && cargo build --release)
git clone https://github.com/rianprei/snapedit
```

Results and interpretation: [CONTRACT.md](CONTRACT.md).


## State

`$ATTO_HOME` (default `$XDG_STATE_HOME/atto`, else `~/.local/state/atto`) holds:

- `snapshots/<rev>` — content-addressed pre/post images, needed for conflict relocation and undo
- `journal.jsonl` — every transaction, also the event stream a viewer would consume

The tool never creates the state directory until a read or patch needs it.
