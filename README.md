# pawn-analysis

Shared Pawn preprocessing and semantic analysis for PawnKit tools.

The library provides original and expanded syntax, source-mapped symbols,
tag and call checks, control flow, constant evaluation, and cached snapshots.
Automaton checks cover invalid state transitions and conflicting state
implementations.

```go
result, err := analysis.AnalyzeContext(ctx, text, analysis.Options{
    URI: source.FileURI(path),
})
```

Use `query.Snapshot` for versioned editor documents. Set `Options.Revision`
when API metadata or resolver state changes so cached results are invalidated.
Use `AnalyzeDocuments` when the project host has already built a dependency
closure.

Run `go test ./...` for the offline suite. Tests use a sibling `pawn-corpus`
checkout when present. Set `PAWN_CORPUS_DIR` when it lives elsewhere.

## Contributing

Small parser, resolver, and analysis fixes are welcome. A reduced Pawn example
is often the best place to start. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Licence

[MIT](LICENSE)
