# Changelog

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
