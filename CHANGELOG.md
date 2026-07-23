# Changelog

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
