// Package preprocess implements the Pawn preprocessor: directive parsing,
// object-like and function-like macro expansion, conditional-compilation
// branch tracking, and source maps from expanded tokens back to the
// original source.
//
// Run tokenizes and processes source once, returning a [Result] that keeps
// three views alive simultaneously, per the ecosystem's "preprocessing is a
// first-class language stage" principle:
//
//   - Original: Result.OriginalTokens, the unmodified lexer output.
//   - Active/inactive: Result.Branches records every #if/#elseif/#else
//     branch with its body span and whether it was selected.
//   - Expanded: Result.ExpandedTokens, the macro-expanded, directive-free
//     token stream ready for github.com/pawnkit/pawn-parser's
//     ParseTokensCompact. Expanded tokens carry a token.Origin chain (spelling
//     location vs. expansion location, in the Clang sense) usable to map a
//     diagnostic on expanded code back to where the responsible macro was
//     invoked.
//
// #include/#tryinclude resolution is delegated to a caller-supplied
// [IncludeResolver] so this package never touches the filesystem or a
// project model directly; see docs/architecture.md for the ownership
// rationale.
package preprocess
