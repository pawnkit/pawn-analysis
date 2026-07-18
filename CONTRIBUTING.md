# Contributing

PawnKit is maintained by volunteers, so reviews may take a little time.

Bug reports with a small Pawn example are especially useful. Fixes should add a
focused test or a reduced `pawn-corpus` fixture when the case is shared with
other tools.

Before opening a pull request, run:

```sh
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...
```

Keep preprocessing, source mapping, and semantic facts here. User-facing lint
policy belongs in `pawnlint`; editor protocol behavior belongs in
`pawn-language-server`.
