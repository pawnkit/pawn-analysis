// Package analysis wires PawnKit preprocessing, parsing, and semantics.
package analysis

import (
	"context"
	"errors"
	"runtime"
	"sync"

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
	Previous        *Result
	Trace           func(TraceEvent)
}

type ReuseStats struct {
	ControlFlow  int
	Declarations int
	Tags         int
}

// Result is one immutable file analysis.
type Result struct {
	File            source.FileID
	Registry        *source.Registry
	Preprocess      *preprocess.Result
	Parse           *parser.CompactFile
	ExpandedParse   *parser.CompactFile
	Declarations    parser.DeclarationIndex
	Symbols         *symbol.Table
	ExpandedSymbols *symbol.Table
	Semantics       sema.Result
	ControlFlow     []sema.FunctionFlow
	Diagnostics     []diagnostic.Diagnostic
	Reuse           ReuseStats
	baseDiagnostics []diagnostic.Diagnostic
	flowCache       *sema.FlowCache
	tagCache        *sema.TagCache
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
	opts.Trace = serializeTrace(opts.Trace)
	uri := opts.URI
	if !uri.IsValid() {
		uri = source.FileURI("input.pwn")
	}
	registry := source.NewRegistry()
	fileID := registry.Intern(uri)

	stage := beginStage(opts.Trace, StagePreprocess)
	pre, err := preprocess.RunContext(ctx, text, preprocess.Options{
		URI:             uri.String(),
		Resolver:        opts.Includes,
		Predefined:      opts.Predefined,
		MaxOutputTokens: opts.MaxOutputTokens,
		TokenCache:      opts.TokenCache,
	})
	stage.end(ctx, 0)
	if err != nil {
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
	var parsed *parser.CompactFile
	var parsedErr error
	var table *symbol.Table
	var expanded *parser.CompactFile
	var expandedErr error
	var expandedTable *symbol.Table
	parseOriginal := func() {
		current := beginStage(opts.Trace, StageParseOriginal)
		parsed, parsedErr = parser.ParseTokensCompactContext(ctx, text, pre.OriginalTokens, parser.ParseOptions{})
		current.end(ctx, 0)
	}
	parseExpanded := func() {
		current := beginStage(opts.Trace, StageParseExpanded)
		expanded, expandedErr = parser.ParseTokensCompactContext(
			ctx, pre.ExpandedSource, pre.ExpandedTokens, parser.ParseOptions{},
		)
		current.end(ctx, 0)
	}
	needsExpanded := opts.RetainExpanded || opts.Includes != nil
	const parallelParseMinimumBytes = 256 * 1024
	if needsExpanded && len(text)+len(pre.ExpandedSource) >= parallelParseMinimumBytes && runtime.GOMAXPROCS(0) > 1 {
		var wait sync.WaitGroup
		wait.Add(1)
		go func() {
			defer wait.Done()
			parseExpanded()
		}()
		parseOriginal()
		wait.Wait()
	} else {
		parseOriginal()
		if needsExpanded {
			parseExpanded()
		}
	}
	if parsedErr != nil {
		return nil, parsedErr
	}
	if expandedErr != nil {
		return nil, expandedErr
	}
	stage = beginStage(opts.Trace, StageSymbolsOriginal)
	table = symbol.Build(parsed.Syntax(), fileID)
	table.Diagnostics = removeMacroDeclarationDiagnostics(table.Diagnostics, pre)
	stage.end(ctx, 0)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if needsExpanded {
		stage = beginStage(opts.Trace, StageSymbolsExpanded)
		expandedTable = symbol.BuildMapped(expanded.Syntax(), fileID, mapFile)
		expandedTable.Diagnostics = removeMacroDeclarationDiagnostics(expandedTable.Diagnostics, pre)
		stage.end(ctx, 0)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	diagnostics := pre.ToRegistryDiagnostics(registry, fileID)
	for _, item := range parsed.Diagnostics {
		diagnostics = append(diagnostics, item.ToCore(fileID))
	}
	diagnostics = append(diagnostics, table.Diagnostics...)
	diagnostics = append(diagnostics, expandedTableDiagnostics(expandedTable, fileID)...)
	addDiagnosticDocs(diagnostics)
	stage = beginStage(opts.Trace, StageDeclarations)
	declarations := parser.BuildDeclarationIndex(parsed)
	reusedDeclarationCount := 0
	if opts.Previous != nil {
		reusedDeclarationCount = reusedDeclarations(opts.Previous.Declarations, declarations)
	}
	stage.end(ctx, reusedDeclarationCount)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prepared := &Result{
		File: fileID, Registry: registry, Preprocess: pre, Parse: parsed, ExpandedParse: expanded,
		Declarations: declarations, Symbols: table, ExpandedSymbols: expandedTable,
		Diagnostics: diagnostics, baseDiagnostics: diagnostics,
	}
	prepared.Reuse.Declarations = reusedDeclarationCount
	if opts.SkipSemantics {
		return retainExpanded(prepared, opts.RetainExpanded), nil
	}
	return CompleteContext(ctx, prepared, opts)
}

func reusedDeclarations(previous, current parser.DeclarationIndex) int {
	if !previous.Reliable() || !current.Reliable() {
		return 0
	}
	fingerprints := make(map[[32]byte][32]byte, previous.Len())
	for position := range previous.Len() {
		item, _ := previous.At(position)
		fingerprints[item.Identity] = item.Fingerprint
	}
	reused := 0
	for position := range current.Len() {
		item, _ := current.At(position)
		fingerprint, found := fingerprints[item.Identity]
		if found && fingerprint == item.Fingerprint {
			reused++
		}
	}
	return reused
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
	stage := beginStage(opts.Trace, StageSemanticNames)
	semantics := sema.CheckNames(prepared.Symbols, resolver)
	stage.end(ctx, 0)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var previousTags *sema.TagCache
	if reusableDeclarations(prepared, opts.Previous) {
		previousTags = opts.Previous.tagCache
	}
	stage = beginStage(opts.Trace, StageSemanticTags)
	tagDiagnostics, tagCache, reusedTags := sema.CheckTagsCached(
		prepared.Parse.Syntax(), prepared.Symbols, resolver, previousTags, opts.Revision,
	)
	stage.end(ctx, reusedTags)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	semantics.Diagnostics = append(semantics.Diagnostics, tagDiagnostics...)
	stage = beginStage(opts.Trace, StageSemanticStates)
	stateDiagnostics := sema.CheckStates(prepared.Parse.Syntax(), prepared.File)
	stage.end(ctx, 0)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	semantics.Diagnostics = append(semantics.Diagnostics, stateDiagnostics...)
	stage = beginStage(opts.Trace, StageSemanticOrder)
	orderDiagnostics := sema.CheckConstantOrder(prepared.Parse.Syntax(), prepared.Symbols)
	stage.end(ctx, 0)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	semantics.Diagnostics = append(semantics.Diagnostics, orderDiagnostics...)
	var previousFlow *sema.FlowCache
	if reusableDeclarations(prepared, opts.Previous) {
		previousFlow = opts.Previous.flowCache
	}
	stage = beginStage(opts.Trace, StageSemanticCFG)
	flows, flowDiagnostics, flowCache, reused := sema.CheckControlFlowCached(
		prepared.Parse.Syntax(), prepared.Symbols, previousFlow,
	)
	stage.end(ctx, reused)
	semantics.Diagnostics = append(semantics.Diagnostics, flowDiagnostics...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := *prepared
	result.Semantics = semantics
	result.ControlFlow = flows
	result.Reuse.ControlFlow = reused
	result.Reuse.Tags = reusedTags
	result.flowCache = flowCache
	result.tagCache = tagCache
	result.Diagnostics = append(append([]diagnostic.Diagnostic(nil), prepared.baseDiagnostics...), semantics.Diagnostics...)
	addDiagnosticDocs(result.Diagnostics)
	return retainExpanded(&result, opts.RetainExpanded), nil
}

func reusableDeclarations(current, previous *Result) bool {
	return current != nil && previous != nil &&
		current.Declarations.Reliable() && previous.Declarations.Reliable()
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
