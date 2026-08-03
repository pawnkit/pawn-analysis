# Testing

Run the offline suite and race detector before changing analysis state or caches:

```sh
go test ./...
CGO_ENABLED=1 go test -race ./...
go vet ./...
```

The suite uses a sibling `pawn-corpus` checkout when present. Set
`PAWN_CORPUS_DIR` when it lives elsewhere. Shared cases stay in the corpus;
small package-level regressions stay here.

Bug fixes need a small regression case. Preprocessor changes should cover malformed input, nesting limits, and source provenance. Cache changes should test clean-result agreement, cancellation, revisions, and concurrent reads.

Golden files never update themselves in CI. External fixtures must record their origin, licence, and pinned version or commit.

## Stage timings

Set `Options.Trace` when profiling a host:

```go
options.Trace = func(event analysis.TraceEvent) {
	log.Printf("%s: %s", event.Stage, event.Duration)
}
```

Tracing is synchronous and disabled by default. Keep callbacks short and do
not use them for application logic.
