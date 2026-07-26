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
completes semantics from those prepared results. A revision key invalidates
results when resolver or API state changes.

Project hosts that know their dependency graph can use `AnalyzeDocuments` to
exclude unrelated files from name resolution.

Top-level declarations have stable IDs based on their signatures, not source
offsets or function bodies. Snapshot updates use them to reuse non-trivial
CFGs. Small graphs are cheaper to rebuild. Function edits invalidate their own
CFG. Changes to resolved global constants invalidate every CFG because they
can change conditional control flow.

Stable IDs are available through `symbol.Table.StableSymbolID`. They are not
stored on local symbols.
