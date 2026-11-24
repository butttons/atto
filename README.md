# atto

Headless, agent-first file editing. One binary, one contract, one pi extension.

Every change to an existing file is a snapshot-guarded, line-addressed, atomic
transaction: read the lines, get a rev, send only the new text, get a diff and
a new rev. No retyping old text, no fuzzy matching, no Python heredocs.

## Install

```sh
go build -o ~/.local/bin/atto ./tools/atto
```

## Use

```sh
atto read FILE [FROM:TO]   # numbered lines + snapshot rev
atto stat FILE             # just the rev
atto patch < plan          # one atomic transaction
atto undo FILE             # revert the last transaction
atto log [FILE]            # recent transactions
```

The plan is line-oriented and literal:

```
file path/to/file
rev <the rev from read>
SWAP 41.=48:
+new line
+another
```

The full contract is [tools/atto/CONTRACT.md](tools/atto/CONTRACT.md).

## pi extension

The extension makes pi edit through atto and blocks `edit`, `write` on existing
files, and Python/sed/perl/tee file edits.

```sh
pi install git:github.com/butttons/atto
```

Put `atto` on PATH (or set `ATTO_BIN`). The extension is declared in
`package.json` and loads from `tools/atto/pi/atto.ts`. To try it without
installing:

```sh
pi -e git:github.com/butttons/atto
```

## Verify

```sh
cd tools/atto
go test ./...
go build -o atto .
cd ../..
python3 tools/atto/bench/bench.py --atto tools/atto/atto
```

The real-tools comparison and its results are in
[tools/atto/CONTRACT.md](tools/atto/CONTRACT.md).
