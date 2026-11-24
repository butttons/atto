# atto

The product is the headless editing binary in `tools/atto` and the pi extension
in `tools/atto/pi`. The contract is `tools/atto/CONTRACT.md`.

## Build and test

```sh
cd tools/atto
go build -o atto .
go test ./...
go vet ./...
```

## Rules

- One mutation path: `atto read` -> snapshot rev -> `atto patch`.
- Address text by 1-based line spans, never by byte offsets.
- Keep the pi gate closed: no `edit`, no `write` on existing files, no Python
  or sed file edits.
- Never commit the built binary; `tools/atto/atto` is ignored.
- Files under 800 non-test lines. Comments say why, not what. No emoji.
