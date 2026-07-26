// Package analysis wires PawnKit preprocessing, parsing, and semantics.
package analysis

import (
	"context"
	"errors"

	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawn-analysis/sema"
	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

// Options configures one file analysis.
type Options struct {
	URI             source.URI
	Includes        preprocess.IncludeResolver
	Names           sema.Resolver
	Predefined      map[string]string
	Revision        string
	RetainExpanded  bool
	MaxOutputTokens int
	SkipSemantics   bool
	TokenCache      *preprocess.TokenCache
}

// Result is one immutable file analysis.
type Result struct {
	File            source.FileID
	Registry        *source.Registry
	Preprocess      *preprocess.Result
	Parse           *parser.CompactFile
	ExpandedParse   *parser.CompactFile
	Symbols         *symbol.Table
	ExpandedSymbols *symbol.Table
	Semantics       sema.Result
	ControlFlow     []sema.FunctionFlow
	Diagnostics     []diagnostic.Diagnostic
	baseDiagnostics []diagnostic.Diagnostic
}

// Analyze runs the shared per-file pipeline.
func Analyze(text []byte, opts Options) *Result {
	result, _ := AnalyzeContext(context.Background(), text, opts)
	return result
}

// AnalyzeContext runs the shared per-file pipeline with cancellation.
func AnalyzeContext(ctx context.Context, text []byte, opts Options) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uri := opts.URI
	if !uri.IsValid() {
		uri = source.FileURI("input.pwn")
	}
	registry := source.NewRegistry()
	fileID := registry.Intern(uri)

	pre := preprocess.Run(text, preprocess.Options{
		URI:             uri.String(),
		Resolver:        opts.Includes,
		Predefined:      opts.Predefined,
		MaxOutputTokens: opts.MaxOutputTokens,
		TokenCache:      opts.TokenCache,
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fileIDs := make([]source.FileID, len(pre.Files))
	for i, file := range pre.Files {
		fileURI := source.URI(file.URI)
		if !fileURI.IsValid() {
			fileURI = uri
		}
		fileIDs[i] = registry.Intern(fileURI)
	}
	mapFile := func(index uint32) source.FileID {
		if int(index) < len(fileIDs) {
			return fileIDs[index]
		}
		return fileID
	}
	parsed := parser.ParseTokensCompact(text, pre.OriginalTokens, parser.ParseOptions{})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	table := symbol.Build(parsed.Syntax(), fileID)
	table.Diagnostics = removeMacroDeclarationDiagnostics(table.Diagnostics, pre)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var expanded *parser.CompactFile
	var expandedTable *symbol.Table
	if opts.RetainExpanded || opts.Includes != nil {
		expanded = parser.ParseTokensCompact(pre.ExpandedSource, pre.ExpandedTokens, parser.ParseOptions{})
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		expandedTable = symbol.BuildMapped(expanded.Syntax(), fileID, mapFile)
		expandedTable.Diagnostics = removeMacroDeclarationDiagnostics(expandedTable.Diagnostics, pre)
	}
	diagnostics := pre.ToRegistryDiagnostics(registry, fileID)
	for _, item := range parsed.Diagnostics {
		diagnostics = append(diagnostics, item.ToCore(fileID))
	}
	diagnostics = append(diagnostics, table.Diagnostics...)
	diagnostics = append(diagnostics, expandedTableDiagnostics(expandedTable, fileID)...)
	addDiagnosticDocs(diagnostics)
	prepared := &Result{
		File: fileID, Registry: registry, Preprocess: pre, Parse: parsed, ExpandedParse: expanded,
		Symbols: table, ExpandedSymbols: expandedTable,
		Diagnostics: diagnostics, baseDiagnostics: diagnostics,
	}
	if opts.SkipSemantics {
		return retainExpanded(prepared, opts.RetainExpanded), nil
	}
	return CompleteContext(ctx, prepared, opts)
}

// CompleteContext runs semantics on an existing parse result.
func CompleteContext(ctx context.Context, prepared *Result, opts Options) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if prepared == nil || prepared.Parse == nil || prepared.Symbols == nil || prepared.Preprocess == nil {
		return nil, errors.New("analysis result is incomplete")
	}
	resolver := newNameResolver(prepared.Preprocess.Macros, prepared.ExpandedSymbols, opts.Names)
	semantics := sema.CheckNames(prepared.Symbols, resolver)
	semantics.Diagnostics = append(semantics.Diagnostics, sema.CheckTags(prepared.Parse.Syntax(), prepared.Symbols, resolver)...)
	semantics.Diagnostics = append(semantics.Diagnostics, sema.CheckStates(prepared.Parse.Syntax(), prepared.File)...)
	semantics.Diagnostics = append(semantics.Diagnostics, sema.CheckConstantOrder(prepared.Parse.Syntax(), prepared.Symbols)...)
	flows, flowDiagnostics := sema.CheckControlFlow(prepared.Parse.Syntax(), prepared.Symbols)
	semantics.Diagnostics = append(semantics.Diagnostics, flowDiagnostics...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := *prepared
	result.Semantics = semantics
	result.ControlFlow = flows
	result.Diagnostics = append(append([]diagnostic.Diagnostic(nil), prepared.baseDiagnostics...), semantics.Diagnostics...)
	addDiagnosticDocs(result.Diagnostics)
	return retainExpanded(&result, opts.RetainExpanded), nil
}

func retainExpanded(result *Result, retain bool) *Result {
	if retain {
		return result
	}
	clone := *result
	clone.ExpandedParse = nil
	clone.ExpandedSymbols = nil
	return &clone
}

func addDiagnosticDocs(items []diagnostic.Diagnostic) {
	for i := range items {
		if items[i].Source == "pawn-analysis" && items[i].DocsURL == "" {
			items[i].DocsURL = "https://github.com/pawnkit/pawn-analysis/blob/main/docs/diagnostics.md"
		}
	}
}

func expandedTableDiagnostics(table *symbol.Table, root source.FileID) []diagnostic.Diagnostic {
	if table == nil {
		return nil
	}
	var items []diagnostic.Diagnostic
	for _, item := range table.Diagnostics {
		if item.Primary.File != root {
			items = append(items, item)
		}
	}
	return items
}

func removeMacroDeclarationDiagnostics(items []diagnostic.Diagnostic, pre *preprocess.Result) []diagnostic.Diagnostic {
	result := items[:0]
	for _, item := range items {
		if item.Code != "pawn-analysis:symbol/redeclared" || !insideMacroInvocation(item.Primary, pre) {
			result = append(result, item)
		}
	}
	return result
}

func insideMacroInvocation(span source.Span, pre *preprocess.Result) bool {
	for _, item := range pre.ExpandedTokens {
		for origin := item.Origin; origin != nil; origin = origin.Parent {
			candidate := origin.Span
			if origin.Macro != "" && candidate.File == 0 && int(span.Start) >= candidate.Start.Offset && int(span.End) <= candidate.End.Offset {
				return true
			}
		}
	}
	return false
}

type nameResolver struct {
	macros      map[string]preprocess.Macro
	symbols     *symbol.Table
	globalNames map[string]symbol.ID
	next        sema.Resolver
}

// newNameResolver reuses the file scope's existing name index instead of
// scanning all symbols on every ResolveName/ResolveCallable call.
func newNameResolver(macros map[string]preprocess.Macro, symbols *symbol.Table, next sema.Resolver) nameResolver {
	var globalNames map[string]symbol.ID
	if symbols != nil {
		for _, scope := range symbols.Scopes {
			if scope.Kind == symbol.ScopeFile {
				globalNames = scope.Names
				break
			}
		}
	}
	return nameResolver{macros: macros, symbols: symbols, globalNames: globalNames, next: next}
}

func (r nameResolver) ResolveName(name string) sema.NameState {
	if _, ok := r.macros[name]; ok {
		return sema.NameFound
	}
	if _, ok := r.global(name); ok {
		return sema.NameFound
	}
	if r.next != nil {
		return r.next.ResolveName(name)
	}
	return sema.NameUnknown
}

func (r nameResolver) ResolveCallable(name string) (sema.Callable, bool) {
	if item, ok := r.global(name); ok && item.Kind.IsCallable() {
		return sema.Callable{
			ReturnTag: item.Tag, ParamTags: item.ParamTags, MinArgs: item.MinArgs, MaxArgs: item.MaxArgs,
		}, true
	}
	if next, ok := r.next.(sema.CallableResolver); ok {
		return next.ResolveCallable(name)
	}
	return sema.Callable{}, false
}

func (r nameResolver) global(name string) (symbol.Symbol, bool) {
	id, ok := r.globalNames[name]
	if !ok {
		return symbol.Symbol{}, false
	}
	return r.symbols.Symbol(id)
}
