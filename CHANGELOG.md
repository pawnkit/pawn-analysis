# Changelog

## 0.30.8 - 2026-08-01

### Changed

- Use pawn-parser v1.5.7.

## 0.30.7 - 2026-08-01

### Fixed

- Resolve PawnPlus tag aliases before checking argument compatibility.

## 0.30.6 - 2026-08-01

### Performance

- Index callable symbols by scope and name before resolving calls.

## 0.30.5 - 2026-08-01

### Performance

- Let editor clients pass the known byte edit and skip a redundant full-file
  trivia scan for code edits.
- Index token overlap checks by source offset.

## 0.30.4 - 2026-08-01

### Performance

- Reuse the token stream for safe same-length literal and identifier edits
  instead of retokenizing the whole source file.

## 0.30.3 - 2026-08-01

### Performance

- Reuse original syntax, symbols, and stable semantic results for trivia-only
  edits, reducing the query benchmark from about 628 ms to 32 ms.

## 0.30.2 - 2026-08-01

### Fixed

- Keep YSI `Test:` entry points separate from production functions.
- Resolve calls to the production function when a test shares its name.

## 0.30.1 - 2026-07-30

### Fixed

- Include native and forward declarations in function effects.
- Preserve `const` on parameter symbols.

## 0.30.0 - 2026-07-30

### Added

- Resolve function effects for calls supplied by an external API.

## 0.29.2 - 2026-07-30

### Performance

- Find parameter reference markers without rescanning the full token stream.

## 0.29.1 - 2026-07-30

### Fixed

- Preserve sparse parameter indexes in mapped function facts.

## 0.29.0 - 2026-07-30

### Added

- Propagate function effects through calls and included files.

## 0.28.0 - 2026-07-30

### Added

- Collect direct function effects for consumers that request them.

## 0.27.1 - 2026-07-30

### Performance

- Reuse symbol resolution when an edit only wraps or unwraps existing text.

## 0.27.0 - 2026-07-30

### Added

- Report when incremental analysis reparses one changed declaration.

### Performance

- Reuse the surrounding compact syntax after token-moving edits inside one declaration.

## 0.26.0 - 2026-07-29

### Added

- Let cached tag checks consume parser declaration fingerprints.

### Performance

- Reuse name, state, and constant-order results for fixed-position local edits.

## 0.25.0 - 2026-07-29

### Added

- Patch a resolved symbol reference without rebuilding the full symbol table.

### Performance

- Reuse symbol data when an editor edit changes one resolved reference.

## 0.24.2 - 2026-07-29

### Added

- Report when incremental analysis rebases syntax after a trivia edit.

## 0.24.1 - 2026-07-29

### Performance

- Reuse compatible preprocessing for small and single-file projects.

## 0.24.0 - 2026-07-29

### Performance

- Rebase original syntax after grammar-neutral whitespace edits.

## 0.23.1 - 2026-07-29

### Performance

- Reuse local symbol tables when a function edit cannot change declarations
  or references.

## 0.23.0 - 2026-07-29

### Performance

- Reuse original syntax when an editor edit keeps the same token layout.

## 0.22.1 - 2026-07-29

### Performance

- Removed the function name cache after editor benchmarks showed higher
  latency and memory use.

## 0.22.0 - 2026-07-29

### Performance

- Reused name and call-arity checks for unchanged functions.

## 0.21.0 - 2026-07-29

### Added

- Retain macro invocation ranges in preprocessing results.
- Benchmark cancellation latency on a generated 50,000-line project.

### Performance

- Stop scanning the expanded token stream when filtering macro-generated
  declaration diagnostics.
- Collect state-analysis facts in one syntax pass.
- Reduced SAFW incremental analysis from about 200-227 ms to 80-126 ms on the
  reference machine.

## 0.20.2 - 2026-07-28

### Added

- Expose the target callable of forwarding macros.

## 0.20.1 - 2026-07-28

### Fixed

- Allow include guards to stop recursive include chains before reporting a
  cycle.
- Skip external argument checks when a macro replaces the callable.

## 0.20.0 - 2026-07-28

### Added

- Expose each preprocessed file's token stream for downstream reuse.

## 0.19.0 - 2026-07-28

### Performance

- Emit nested macro expansions directly into the final token stream.
- Reuse macro body origins and grow the bounded output buffer in larger steps.
- Reduced the SAFW cold workspace diagnostic response from about 7.2 seconds
  to 3.9 seconds on the reference machine.

## 0.18.0 - 2026-07-28

### Performance

- Map expanded symbols from the retained preprocessor tokens instead of
  keeping a second token copy in the parser.
- Reduced the SAFW cold workspace diagnostic response from about 10.7 seconds
  to 7.2 seconds on the reference machine.

## 0.17.0 - 2026-07-28

### Performance

- Extend compatible dependency-graph reuse to local insertions and deletions.
- Shift later directive ranges when a local edit changes source length.

## 0.16.0 - 2026-07-28

### Added

- Editor analysis can reuse a compatible expanded dependency graph for safe,
  same-width function-body edits.

### Performance

- SAFW local analysis fell from about 8.5 seconds to 0.25-0.40 seconds on the
  reference machine.

## 0.15.0 - 2026-07-26

### Performance

- Reuse preprocessing when an edit changes only fixed-position trivia.

## 0.14.2 - 2026-07-26

### Performance

- Reuse expanded parsing and symbols when an edit leaves tokens and origins unchanged.

## 0.14.1 - 2026-07-26

### Fixed

- Keep active-file locals and references in the expanded navigation table.

## 0.14.0 - 2026-07-26

### Performance

- Build only top-level declarations for expanded include graphs.
- Index macro invocation ranges once when filtering redeclarations.

## 0.13.1 - 2026-07-26

### Performance

- Stop tokenizing root and included files after cancellation.

## 0.13.0 - 2026-07-26

### Added

- Allow state checks, constant analysis, and CFG construction to stop during
  cancellation.

## 0.12.0 - 2026-07-26

### Added

- Allow name resolution and tag checking to stop during cancellation.

## 0.11.0 - 2026-07-26

### Added

- Allow symbol construction and indexing to stop during cancellation.

## 0.10.1 - 2026-07-26

### Performance

- Skip remaining symbol and semantic stages after cancellation.

## 0.10.0 - 2026-07-26

### Added

- Allow preprocessing and macro expansion to stop when analysis is cancelled.

## 0.9.0 - 2026-07-26

### Changed

- Stop original and expanded parsing when editor analysis is cancelled.

## 0.8.1 - 2026-07-26

### Testing

- Compare persistent snapshot results with clean analysis across edit sequences.

### Fixed

- Disable semantic reuse when syntax recovery cannot prove safe boundaries.

## 0.8.0 - 2026-07-26

### Added

- Analysis snapshots can retain persistent source buffers until analysis starts.

## 0.7.1 - 2026-07-26

### Added

- Report declaration indexing and reuse in analysis traces.

## 0.7.0 - 2026-07-26

### Added

- Track unchanged top-level declarations between analysis revisions.

## 0.6.2 - 2026-07-26

### Performance

- Kept symbol construction sequential after concurrent parsing to reduce
  memory contention.
- Reduced the traced 50,000-line LSP benchmark from 453–485 ms to 382–423 ms.

## 0.6.1 - 2026-07-26

### Performance

- Parsed and indexed original and expanded syntax concurrently for large
  inputs.
- Reduced the expanded 2,000-function benchmark from 232–257 ms to 169–184
  ms.

## 0.6.0 - 2026-07-26

### Added

- Added opt-in timings for preprocessing, parsing, symbol building, semantics,
  and control flow.

## 0.5.2 - 2026-07-26

### Changed

- Skipped tag-cache setup when a file has no cacheable functions.

## 0.5.1 - 2026-07-26

### Changed

- Limited tag-result caching to functions large enough to repay its memory
  cost.

## 0.5.0 - 2026-07-26

### Added

- Reused tag checks for unchanged functions when exports and resolver data are
  unchanged.

### Performance

- Reduced the large incremental-analysis benchmark from about 320,000
  allocations to 250,000.

## 0.4.3 - 2026-07-26

### Fixed

- Capped preprocessor token preallocation at the configured output limit.

## 0.4.2 - 2026-07-26

### Changed

- Pre-sized preprocessor output buffers to reduce allocation growth.

## 0.4.1 - 2026-07-26

### Changed

- Stopped hashing full document contents for immutable snapshot cache lookups.
- Added a large cached-analysis benchmark.

## 0.4.0 - 2026-07-26

### Added

- Added `query.Snapshot.UpdateOwned` for immutable editor buffers.

### Changed

- Avoided a 476 KB copy per revision in the large snapshot benchmark.

## 0.3.1 - 2026-07-26

### Changed

- Reused the symbol table's span indexes during tag checks.

## 0.3.0 - 2026-07-26

### Changed

- Built stable IDs and export fingerprints only when incremental analysis asks
  for them. Use `Table.ExportFingerprint` instead of the former field.

## 0.2.0 - 2026-07-26

### Changed

- Moved stable declaration IDs from every symbol into the top-level symbol
  index. Use `Table.StableSymbolID` to read them.

## 0.1.24 - 2026-07-26

### Changed

- Avoided body hashing for functions that are not CFG cache candidates.

## 0.1.23 - 2026-07-26

### Changed

- Limited CFG caching to functions where reuse costs less than rebuilding.

## 0.1.22 - 2026-07-26

### Changed

- Reduced allocations while building stable symbol IDs.

## 0.1.21 - 2026-07-26

### Added

- Added stable top-level symbol IDs and export fingerprints.
- Reused unchanged function control-flow graphs across snapshot revisions.

## 0.1.20 - 2026-07-26

### Changed

- Workspace analysis now reuses its first parse when resolving shared names.

## 0.1.19 - 2026-07-26

### Changed

- Indexed declaration and reference spans used by semantic checks.

## 0.1.18 - 2026-07-25

### Added

- Documented preview package boundaries and added a compatibility compile test.
- Published the repository support record.

## 0.1.17 - 2026-07-24

### Fixed

- Stopped re-tokenizing the entry file a second time in `AnalyzeContext`; it
  now reuses the token stream `preprocess.Run` already built.

## 0.1.16 - 2026-07-24

### Added

- Added `preprocess.TokenCache` and `Options.TokenCache`, which reuse an
  include file's token stream across calls when its content hasn't changed.

## 0.1.15 - 2026-07-24

### Fixed

- Removed a duplicate tokenization pass over the source in `preprocess.Run`.
- Replaced a linear scan of all global symbols in name resolution with a map lookup.
- Replaced a linear scan of all symbols and references in `sema.CheckTags` with a span index.
- Added `Options.SkipSemantics` and used it in workspace-resolver indexing to
  skip semantic checks whose results were discarded.

## 0.1.14 - 2026-07-23

### Added

- Checked cache invalidation for included files and profile defines.

### Fixed

- Stopped treating tag names and `_:` overrides as symbol references.
- Made corpus-backed query tests resolve the configured checkout correctly.

## 0.1.13 - 2026-07-23

### Added

- Checked clean and incremental analysis against the shared preprocessor corpus.

## 0.1.12 - 2026-07-23

### Added

- Linked analysis diagnostics to a short troubleshooting reference.

## 0.1.11 - 2026-07-23

### Fixed

- Updated the Pawn parser for current macro and include syntax.

## 0.1.10 - 2026-07-22

### Fixed

- Stop requiring return values from `void:` functions.
- Ignore YSI iterator capacities when checking Pawn state variables.

## 0.1.9 - 2026-07-22

### Fixed

- Treat YSI `va_args<>` parameters as variadic.

## 0.1.8 - 2026-07-21

### Added

- Added an optional token limit for expanded analysis output.

## 0.1.7 - 2026-07-21

- Reported declaration conflicts from active included files.

## 0.1.6 - 2026-07-21

### Fixed

- Expanded tag macros before checking calls across workspace files.

## 0.1.5 - 2026-07-21

### Fixed

- Bound sparse macro parameter labels in declaration order.

## 0.1.4 - 2026-07-21

### Fixed

- Treated concise function bodies such as `stock Tag:Name() return value;` as returning control flow.

## 0.1.3 - 2026-07-21

### Fixed

- Handle PawnPlus generic tags, declaration macros, and conditional `else if` splices through pawn-parser v1.1.4.

## 0.1.2 - 2026-07-21

### Fixed

- Accepted `@` callback declarations with `const` array parameters.
