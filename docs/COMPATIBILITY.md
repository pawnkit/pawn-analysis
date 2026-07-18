# Compatibility

Public packages follow semantic versioning after the first stable release.
Before v1, exported API changes require migration notes in the release.

Diagnostic codes are stable identifiers. Messages may improve without a major
version change. Unknown external names remain unknown unless a resolver confirms
that they are missing.

Compiler compatibility cases live in `compatibility_test.go`. Corpus tests read
fixture IDs and expectations from `pawn-corpus`; `PAWN_CORPUS_DIR` can point to
a non-sibling checkout.
