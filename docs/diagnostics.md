# Diagnostics

Pawn analysis reports problems found while resolving includes, expanding
macros, building symbols, and checking semantics. The diagnostic code names
the stage and condition, for example `pawn-analysis:sema/argument-count`.

## Preprocessor

Codes beginning with `preprocess/` come from directives, macros, or includes.
For a missing include, check the path in `pawn.json` and whether the include
uses project-relative or include-root lookup. Expansion limit diagnostics
usually point to a recursive macro or include cycle.

Conditions that depend on compiler-defined values may need a target profile or
predefined value in the project configuration.

## Symbols

`symbol/redeclared` means two declarations resolve to the same name and scope.
Rename one declaration, remove the duplicate, or check whether a macro expands
to declarations unexpectedly.

## Semantics

Codes beginning with `sema/` cover calls, tags, control flow, constants, and
Pawn automata. Check the diagnostic message first; it includes the symbol,
expected argument count, tag, state, or label when that information is known.

Some external calls remain intentionally unresolved when no API or project
declaration is available. Select the correct target profile and include roots
before treating an unresolved symbol as a source error.

If a diagnostic looks wrong, include its full code and a small source sample in
the bug report. Include the project profile and relevant include paths for
preprocessor issues.
