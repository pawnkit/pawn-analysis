// Package analysis wires PawnKit preprocessing, parsing, and semantics.
package analysis

import (
	"context"
	"errors"
	"runtime"
	"sort"
	"sync"

	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawn-analysis/sema"
	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawn-parser/token"
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
	// ReuseCompatibleExpansion allows editor diagnostics to reuse an unchanged
	// dependency graph. Expanded source may describe the previous local body.
	ReuseCompatibleExpansion bool
	Trace                    func(TraceEvent)
}

type ReuseStats struct {
	ControlFlow         int
	Declarations        int
	Tags                int
	CompatibleExpansion bool
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
	revision        string
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
	var pre *preprocess.Result
	var err error
	reusedPreprocess := false
	var localChanges []preprocess.ByteRange
	localCandidate := false
	if opts.Previous != nil && opts.Revision != "" && opts.Previous.revision == opts.Revision {
		pre, reusedPreprocess, err = preprocess.ReuseTriviaContext(
			ctx, text, uri.String(), opts.TokenCache, opts.Previous.Preprocess,
		)
		if err == nil && !reusedPreprocess && opts.ReuseCompatibleExpansion &&
			worthwhileLocalReuse(opts.Previous.Preprocess) {
			pre, localChanges, localCandidate, err = preprocess.ReuseCompatibleContext(
				ctx, text, uri.String(), opts.TokenCache, opts.Previous.Preprocess,
			)
			reusedPreprocess = localCandidate
		}
	}
	if err == nil && !reusedPreprocess {
		pre, err = preprocess.RunContext(ctx, text, preprocess.Options{
			URI:             uri.String(),
			Resolver:        opts.Includes,
			Predefined:      opts.Predefined,
			MaxOutputTokens: opts.MaxOutputTokens,
			TokenCache:      opts.TokenCache,
		})
	}
	if reusedPreprocess {
		stage.end(ctx, 1)
	} else {
		stage.end(ctx, 0)
	}
	if err != nil {
		return nil, err
	}
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
	var parsed *parser.CompactFile
	var parsedErr error
	var table *symbol.Table
	var expanded *parser.CompactFile
	var expandedErr error
	var expandedTable *symbol.Table
	reusedExpanded := reusableExpanded(pre, opts.Previous)
	if reusedExpanded {
		expanded = opts.Previous.ExpandedParse
		expandedTable = opts.Previous.ExpandedSymbols
	}
	parseOriginal := func() {
		current := beginStage(opts.Trace, StageParseOriginal)
		parsed, parsedErr = parser.ParseTokensCompactContext(ctx, text, pre.OriginalTokens, parser.ParseOptions{})
		current.end(ctx, 0)
	}
	parseExpanded := func() {
		current := beginStage(opts.Trace, StageParseExpanded)
		if reusedExpanded {
			current.end(ctx, 1)
			return
		}
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
	table, err = symbol.BuildContext(ctx, parsed.Syntax(), fileID)
	if err != nil {
		stage.end(ctx, 0)
		return nil, err
	}
	stage.end(ctx, 0)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if needsExpanded {
		stage = beginStage(opts.Trace, StageSymbolsExpanded)
		if reusedExpanded {
			stage.end(ctx, 1)
		} else {
			expandedTable, err = symbol.BuildMappedNavigationContext(ctx, expanded.Syntax(), fileID, mapFile)
			if err != nil {
				stage.end(ctx, 0)
				return nil, err
			}
			stage.end(ctx, 0)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if hasRedeclarationDiagnostics(table) || hasRedeclarationDiagnostics(expandedTable) {
		invocations := macroInvocationRanges(pre)
		table.Diagnostics = removeMacroDeclarationDiagnostics(table.Diagnostics, invocations)
		if expandedTable != nil {
			expandedTable.Diagnostics = removeMacroDeclarationDiagnostics(expandedTable.Diagnostics, invocations)
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
	if localCandidate && !reusableLocalEdit(opts.Previous, parsed, declarations, table, localChanges) {
		fallback := opts
		fallback.Previous = nil
		return AnalyzeContext(ctx, text, fallback)
	}
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
		revision: opts.Revision,
	}
	prepared.Reuse.Declarations = reusedDeclarationCount
	prepared.Reuse.CompatibleExpansion = localCandidate
	if opts.SkipSemantics {
		return retainExpanded(prepared, opts.RetainExpanded), nil
	}
	return CompleteContext(ctx, prepared, opts)
}

func worthwhileLocalReuse(previous *preprocess.Result) bool {
	if previous == nil {
		return false
	}
	return len(previous.ExpandedTokens) > len(previous.OriginalTokens)*2
}

func reusableExpanded(current *preprocess.Result, previous *Result) bool {
	if current == nil || previous == nil || previous.Preprocess == nil ||
		previous.ExpandedParse == nil || previous.ExpandedSymbols == nil {
		return false
	}
	before := previous.Preprocess
	if len(current.ExpandedSource) != len(before.ExpandedSource) ||
		len(current.ExpandedTokens) != len(before.ExpandedTokens) ||
		len(current.Files) != len(before.Files) {
		return false
	}
	for i := range current.Files {
		if current.Files[i].URI != before.Files[i].URI {
			return false
		}
	}
	if sameByteBacking(current.ExpandedSource, before.ExpandedSource) &&
		sameTokenBacking(current.ExpandedTokens, before.ExpandedTokens) {
		return true
	}
	for i := range current.ExpandedSource {
		if current.ExpandedSource[i] != before.ExpandedSource[i] {
			return false
		}
	}
	for i := range current.ExpandedTokens {
		if !sameExpandedToken(current.ExpandedTokens[i], before.ExpandedTokens[i]) {
			return false
		}
	}
	return true
}

func sameByteBacking(left, right []byte) bool {
	return len(left) == 0 && len(right) == 0 ||
		len(left) != 0 && len(right) != 0 && &left[0] == &right[0]
}

func sameTokenBacking(left, right []token.Token) bool {
	return len(left) == 0 && len(right) == 0 ||
		len(left) != 0 && len(right) != 0 && &left[0] == &right[0]
}

func sameExpandedToken(left, right token.Token) bool {
	if left.Kind != right.Kind || left.Start != right.Start || left.End != right.End {
		return false
	}
	for left.Origin != nil && right.Origin != nil {
		if left.Origin.Span != right.Origin.Span || left.Origin.Macro != right.Origin.Macro {
			return false
		}
		left.Origin = left.Origin.Parent
		right.Origin = right.Origin.Parent
	}
	return left.Origin == nil && right.Origin == nil
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

func reusableLocalEdit(
	previous *Result,
	currentParse *parser.CompactFile,
	current parser.DeclarationIndex,
	currentSymbols *symbol.Table,
	changes []preprocess.ByteRange,
) bool {
	if previous == nil || previous.Symbols == nil || currentSymbols == nil ||
		!previous.Declarations.Reliable() || !current.Reliable() ||
		previous.Declarations.Len() != current.Len() ||
		previous.Symbols.ExportFingerprint() != currentSymbols.ExportFingerprint() {
		return false
	}
	if changesTouchMacroInvocations(currentParse.Syntax(), changes, previous.Preprocess.Macros) {
		return false
	}
	for _, change := range changes {
		found := false
		for position := range current.Len() {
			now, _ := current.At(position)
			before, _ := previous.Declarations.At(position)
			if now.Identity != before.Identity ||
				now.Kind != parser.KindFunctionDefinition ||
				before.Kind != parser.KindFunctionDefinition {
				continue
			}
			if change.Start >= now.Range.Start && change.End <= now.Range.End &&
				change.Start >= before.Range.Start && change.End <= before.Range.End {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(changes) != 0
}

func changesTouchMacroInvocations(
	root parser.SyntaxNode,
	changes []preprocess.ByteRange,
	macros map[string]preprocess.Macro,
) bool {
	declarations := root.Declarations()
	for declarations.Next() {
		declaration := declarations.Declaration()
		rng := declaration.Range()
		for _, change := range changes {
			if change.Start >= rng.Start && change.End <= rng.End &&
				changesTouchMacroInvocation(declaration, []preprocess.ByteRange{change}, macros) {
				return true
			}
		}
	}
	return false
}

func changesTouchMacroInvocation(
	node parser.SyntaxNode,
	changes []preprocess.ByteRange,
	macros map[string]preprocess.Macro,
) bool {
	if !node.Valid() {
		return false
	}
	rng := node.Range()
	overlaps := false
	for _, change := range changes {
		if change.Start < rng.End && change.End > rng.Start {
			overlaps = true
			break
		}
	}
	if !overlaps {
		return false
	}
	if node.Kind() == parser.KindMacroInvocation {
		return true
	}
	if node.Kind() == parser.KindCallExpression {
		if name, ok := node.Field("function"); ok {
			if _, macro := macros[name.Token().Text()]; macro {
				return true
			}
		}
	}
	children := node.Children()
	for children.Next() {
		if changesTouchMacroInvocation(children.Node(), changes, macros) {
			return true
		}
	}
	return false
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
	semantics, err := sema.CheckNamesContext(ctx, prepared.Symbols, resolver)
	stage.end(ctx, 0)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var previousTags *sema.TagCache
	if reusableDeclarations(prepared, opts.Previous) {
		previousTags = opts.Previous.tagCache
	}
	stage = beginStage(opts.Trace, StageSemanticTags)
	tagDiagnostics, tagCache, reusedTags, err := sema.CheckTagsCachedContext(
		ctx,
		prepared.Parse.Syntax(), prepared.Symbols, resolver, previousTags, opts.Revision,
	)
	stage.end(ctx, reusedTags)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	semantics.Diagnostics = append(semantics.Diagnostics, tagDiagnostics...)
	stage = beginStage(opts.Trace, StageSemanticStates)
	stateDiagnostics, err := sema.CheckStatesContext(ctx, prepared.Parse.Syntax(), prepared.File)
	stage.end(ctx, 0)
	if err != nil {
		return nil, err
	}
	semantics.Diagnostics = append(semantics.Diagnostics, stateDiagnostics...)
	stage = beginStage(opts.Trace, StageSemanticOrder)
	orderDiagnostics, err := sema.CheckConstantOrderContext(ctx, prepared.Parse.Syntax(), prepared.Symbols)
	stage.end(ctx, 0)
	if err != nil {
		return nil, err
	}
	semantics.Diagnostics = append(semantics.Diagnostics, orderDiagnostics...)
	var previousFlow *sema.FlowCache
	if reusableDeclarations(prepared, opts.Previous) {
		previousFlow = opts.Previous.flowCache
	}
	stage = beginStage(opts.Trace, StageSemanticCFG)
	flows, flowDiagnostics, flowCache, reused, err := sema.CheckControlFlowCachedContext(
		ctx,
		prepared.Parse.Syntax(), prepared.Symbols, previousFlow,
	)
	stage.end(ctx, reused)
	semantics.Diagnostics = append(semantics.Diagnostics, flowDiagnostics...)
	if err != nil {
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

func hasRedeclarationDiagnostics(table *symbol.Table) bool {
	if table == nil {
		return false
	}
	for _, item := range table.Diagnostics {
		if item.Code == "pawn-analysis:symbol/redeclared" {
			return true
		}
	}
	return false
}

func removeMacroDeclarationDiagnostics(
	items []diagnostic.Diagnostic,
	invocations []preprocess.ByteRange,
) []diagnostic.Diagnostic {
	result := items[:0]
	for _, item := range items {
		if item.Code != "pawn-analysis:symbol/redeclared" || !insideMacroInvocation(item.Primary, invocations) {
			result = append(result, item)
		}
	}
	return result
}

func macroInvocationRanges(pre *preprocess.Result) []preprocess.ByteRange {
	if pre == nil {
		return nil
	}
	unique := make(map[preprocess.ByteRange]struct{})
	for _, item := range pre.ExpandedTokens {
		for origin := item.Origin; origin != nil; origin = origin.Parent {
			candidate := origin.Span
			if origin.Macro != "" && candidate.File == 0 {
				unique[preprocess.ByteRange{Start: candidate.Start.Offset, End: candidate.End.Offset}] = struct{}{}
			}
		}
	}
	ranges := make([]preprocess.ByteRange, 0, len(unique))
	for item := range unique {
		ranges = append(ranges, item)
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start != ranges[j].Start {
			return ranges[i].Start < ranges[j].Start
		}
		return ranges[i].End < ranges[j].End
	})
	merged := ranges[:0]
	for _, item := range ranges {
		if len(merged) == 0 || item.Start > merged[len(merged)-1].End {
			merged = append(merged, item)
			continue
		}
		merged[len(merged)-1].End = max(merged[len(merged)-1].End, item.End)
	}
	return merged
}

func insideMacroInvocation(span source.Span, ranges []preprocess.ByteRange) bool {
	start, end := int(span.Start), int(span.End)
	index := sort.Search(len(ranges), func(i int) bool { return ranges[i].Start > start })
	if index == 0 {
		return false
	}
	candidate := ranges[index-1]
	if start >= candidate.Start && end <= candidate.End {
		return true
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
