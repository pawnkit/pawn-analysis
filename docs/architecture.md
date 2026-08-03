# Architecture

`pawn-analysis` owns preprocessing, symbols, and semantics. Parsing stays in `pawn-parser`; source locations and diagnostics stay in `pawnkit-core`.

## Data flow

```text
source -> preprocess -> pawn-parser -> symbol table -> semantic checks
```

Includes enter through a narrow resolver interface. Host API names use an optional semantic resolver. Missing context produces unknown results, not false errors.

Original syntax and symbols retain editor offsets. Expanded results are opt-in because their offsets address synthesized text and require another parse.

## Current scope

- `preprocess`: directives, conditionals, includes, macros, and provenance.
- `symbol`: per-file declarations, scopes, and references.
- `sema`: resolver-aware name checks.
- root package: the shared per-file pipeline.

The query package caches analysis across immutable, versioned snapshots. Its
workspace pass resolves file-scope declarations across every document in the
same snapshot. It prepares syntax once, builds the shared name resolver, then
completes semantics from those prepared results. A changed document carries its
matching completed result forward, so unchanged function checks can be reused
without reopening the file. Snapshot-local resolver indexes also survive body
edits when every document keeps the same symbol table; declaration, include,
or resolver changes rebuild the index. A revision key invalidates results when
resolver or API state changes.

Project hosts that know their dependency graph can use `AnalyzeDocuments` to
exclude unrelated files from name resolution.

Top-level declarations have stable IDs based on their signatures, not source
offsets or function bodies. Snapshot updates use them to reuse non-trivial
CFGs and name checks. Small graphs and short functions are cheaper to rebuild.
Function edits invalidate their own cached results. Changes to resolved global
constants invalidate every CFG because they can change conditional control
flow.

Stable IDs are available through `symbol.Table.StableSymbolID`. They are not
stored on local symbols. `symbol.Table.ExportFingerprint` computes the matching
file-level fingerprint. Both indexes are lazy.

## Invalidation

Function-body edits may reuse the include graph, unchanged name and tag checks,
resolved constants, and unchanged CFGs when the edited function has no local
constant declarations. Edits that keep the same token kinds and positions also
reuse the original syntax tree. Function edits that do not touch identifiers
also reuse the symbol table. Macro calls are indexed during preprocessing so
this check does not scan the expanded token stream.

Compatible body edits reuse preprocessing even when the expanded source is
small. The same invalidation checks apply to single-file and include-heavy
projects.

Whitespace edits may shift a clean syntax tree when token text and
parser-significant trivia stay unchanged. Any ambiguous edit falls back to a
full parse.

Trivia-only edits keep the original syntax, symbol table, and stable semantic
checks. The current source buffer is still attached to the reused syntax, so
locations and diagnostics use the new text.

Safe same-length token edits also share the previous immutable token slice;
offsets and kinds stay unchanged while token text comes from the new source.

The following changes force wider work:

- Macro definitions and conditional directives rebuild preprocessing.
- Include content changes require a new resolver revision.
- Profile defines and API data require a new analysis revision.
- Exported declarations invalidate resolver-dependent semantic results and
  function caches.
- Resolved global constant changes invalidate every CFG.

When an edit cannot be classified safely, analysis falls back to a clean run.
Agreement tests compare incremental results with that clean result.
