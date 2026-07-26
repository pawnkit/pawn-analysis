# Compatibility

Public packages follow semantic versioning after the first stable release.
Before v1, exported API changes require migration notes in the release.

Diagnostic codes are stable identifiers. Messages may improve without a major
version change. Unknown external names remain unknown unless a resolver confirms
that they are missing.

Compiler compatibility cases live in `compatibility_test.go`. Corpus tests read
fixture IDs and expectations from `pawn-corpus`; `PAWN_CORPUS_DIR` can point to
a non-sibling checkout.

## API lifecycle

The root `analysis` package is preview until the module reaches v1. Its request,
result, symbol, reference, source-map, diagnostic, and cancellation behavior
is the supported integration path and receives migration notes when it changes.

The `preprocess`, `sema`, `symbol`, `cfg`, and `query` packages are preview.
They are public so PawnKit tools can share work while their boundaries settle.
Downstream projects should prefer the root package unless they need a specific
stage.

Everything under `internal` is internal and has no compatibility promise.
Diagnostic codes remain stable under RFC 0004 even while the Go packages are
preview.
