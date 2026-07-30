package sema

import (
	"sort"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawn-parser/token"
	"github.com/pawnkit/pawnkit-core/source"
)

// FunctionFacts describes effects visible within one function body.
type FunctionFacts struct {
	Complete            bool
	IntrinsicImpure     bool
	ReadsGlobals        []symbol.ID
	WritesGlobals       []symbol.ID
	MutatedParameters   []int
	Calls               []symbol.ID
	CallSites           []FunctionCall
	Parameters          []symbol.ID
	ReferenceParameters []bool
}

// FunctionCall records resolved arguments for one call.
type FunctionCall struct {
	Function  symbol.ID
	Arguments []symbol.ID
}

// BuildFunctionFacts collects direct effects without following calls.
func BuildFunctionFacts(file *parser.CompactFile, table *symbol.Table) map[symbol.ID]FunctionFacts {
	return buildFunctionFacts(
		file,
		table,
		func(node parser.SyntaxNode) (source.Span, bool) {
			return node.Range().Span(table.File), true
		},
		func(within parser.ByteRange) bool {
			return compactRangeHasToken(file.Tokens, within, token.Amp)
		},
	)
}

// BuildMappedFunctionFacts collects effects from expanded syntax.
func BuildMappedFunctionFacts(
	file *parser.CompactFile,
	table *symbol.Table,
	tokens []token.Token,
	mapSpan func(parser.SyntaxNode) (source.Span, bool),
) map[symbol.ID]FunctionFacts {
	return buildFunctionFacts(
		file,
		table,
		mapSpan,
		func(within parser.ByteRange) bool {
			return tokenRangeHasToken(tokens, within, token.Amp)
		},
	)
}

func buildFunctionFacts(
	file *parser.CompactFile,
	table *symbol.Table,
	spanOf func(parser.SyntaxNode) (source.Span, bool),
	hasReferenceMark func(parser.ByteRange) bool,
) map[symbol.ID]FunctionFacts {
	if file == nil || table == nil {
		return nil
	}
	references := make(map[source.Span]symbol.Reference, len(table.References))
	for _, reference := range table.References {
		references[reference.Span] = reference
	}
	result := make(map[symbol.ID]FunctionFacts)
	declarations := file.Syntax().Declarations()
	for declarations.Next() {
		function := declarations.Declaration()
		if function.Kind() != parser.KindFunctionDefinition {
			continue
		}
		name, ok := function.Field("name")
		if !ok {
			continue
		}
		nameSpan, mapped := spanOf(name)
		if !mapped {
			continue
		}
		declared, ok := table.DeclarationAt(nameSpan)
		if !ok || !declared.Kind.IsCallable() {
			continue
		}
		result[declared.ID] = collectFunctionFacts(
			function, declared, table, references, spanOf, hasReferenceMark,
		)
	}
	return result
}

// ResolveFunctionFacts propagates effects through resolved calls.
func ResolveFunctionFacts(
	direct map[symbol.ID]FunctionFacts,
	table *symbol.Table,
) map[symbol.ID]FunctionFacts {
	if len(direct) == 0 || table == nil {
		return direct
	}
	result := make(map[symbol.ID]FunctionFacts, len(direct))
	for id, facts := range direct {
		result[id] = cloneFunctionFacts(facts)
	}
	for changed := true; changed; {
		changed = false
		for id, current := range result {
			reads := idSet(current.ReadsGlobals)
			writes := idSet(current.WritesGlobals)
			mutated := indexSet(current.MutatedParameters)
			complete := current.Complete
			impure := current.IntrinsicImpure
			for _, call := range current.CallSites {
				callee, ok := result[call.Function]
				if !ok || !callee.Complete {
					complete = false
					continue
				}
				impure = impure || callee.IntrinsicImpure
				addIDs(reads, callee.ReadsGlobals)
				addIDs(writes, callee.WritesGlobals)
				if !propagateMutations(call, callee, current, table, writes, mutated) {
					complete = false
				}
			}
			nextReads, nextWrites := sortedIDs(reads), sortedIDs(writes)
			nextMutated := sortedIndexes(mutated)
			if complete != current.Complete || impure != current.IntrinsicImpure ||
				!equalIDs(nextReads, current.ReadsGlobals) ||
				!equalIDs(nextWrites, current.WritesGlobals) ||
				!equalIndexes(nextMutated, current.MutatedParameters) {
				current.Complete = complete
				current.IntrinsicImpure = impure
				current.ReadsGlobals = nextReads
				current.WritesGlobals = nextWrites
				current.MutatedParameters = nextMutated
				result[id] = current
				changed = true
			}
		}
	}
	return result
}

func propagateMutations(
	call FunctionCall,
	callee, caller FunctionFacts,
	table *symbol.Table,
	writes map[symbol.ID]struct{},
	mutated map[int]struct{},
) bool {
	complete := true
	callerParameters := make(map[symbol.ID]int, len(caller.Parameters))
	for index, id := range caller.Parameters {
		if index < len(caller.ReferenceParameters) && caller.ReferenceParameters[index] {
			callerParameters[id] = index
		}
	}
	for _, index := range callee.MutatedParameters {
		if index < 0 || index >= len(call.Arguments) || call.Arguments[index] == 0 {
			complete = false
			continue
		}
		id := call.Arguments[index]
		if parameter, ok := callerParameters[id]; ok {
			mutated[parameter] = struct{}{}
			continue
		}
		item, ok := table.Symbol(id)
		if !ok {
			complete = false
			continue
		}
		scope, ok := table.Scope(item.Scope)
		if ok && scope.Kind == symbol.ScopeFile && !item.Kind.IsCallable() {
			writes[id] = struct{}{}
		}
	}
	return complete
}

func cloneFunctionFacts(facts FunctionFacts) FunctionFacts {
	facts.ReadsGlobals = append([]symbol.ID(nil), facts.ReadsGlobals...)
	facts.WritesGlobals = append([]symbol.ID(nil), facts.WritesGlobals...)
	facts.MutatedParameters = append([]int(nil), facts.MutatedParameters...)
	facts.Calls = append([]symbol.ID(nil), facts.Calls...)
	facts.Parameters = append([]symbol.ID(nil), facts.Parameters...)
	facts.ReferenceParameters = append([]bool(nil), facts.ReferenceParameters...)
	facts.CallSites = append([]FunctionCall(nil), facts.CallSites...)
	for index := range facts.CallSites {
		facts.CallSites[index].Arguments = append([]symbol.ID(nil), facts.CallSites[index].Arguments...)
	}
	return facts
}

func collectFunctionFacts(
	function parser.SyntaxNode,
	declared symbol.Symbol,
	table *symbol.Table,
	references map[source.Span]symbol.Reference,
	spanOf func(parser.SyntaxNode) (source.Span, bool),
	hasReferenceMark func(parser.ByteRange) bool,
) FunctionFacts {
	facts := FunctionFacts{Complete: !function.HasError()}
	parameters := functionParameters(function, table, spanOf, hasReferenceMark)
	reads := make(map[symbol.ID]struct{})
	writes := make(map[symbol.ID]struct{})
	mutated := make(map[int]struct{})
	calls := make(map[symbol.ID]struct{})

	for _, item := range table.Symbols {
		if item.IsStatic && scopeWithin(table, item.Scope, declared.FuncScope) {
			facts.IntrinsicImpure = true
		}
	}
	var visit func(parser.SyntaxNode, []parser.SyntaxNode)
	visit = func(node parser.SyntaxNode, parents []parser.SyntaxNode) {
		switch node.Kind() {
		case parser.KindMacroInvocation, parser.KindMacroInvocationBlock, parser.KindConditionalSplice:
			facts.Complete = false
		case parser.KindStateStatement:
			facts.IntrinsicImpure = true
		case parser.KindFunctionDefinition:
			if node.Range() != function.Range() {
				facts.Complete = false
				return
			}
		case parser.KindCallExpression:
			if call, ok := functionCall(node, table, references, spanOf); ok {
				facts.CallSites = append(facts.CallSites, call)
			}
		case parser.KindIdentifier:
			span, mapped := spanOf(node)
			reference, ok := references[span]
			if mapped && ok {
				applyReferenceFacts(&facts, reference, accessFor(node, parents), table, parameters, reads, writes, mutated, calls)
			}
		}
		nextParents := append(parents, node)
		children := node.Children()
		for children.Next() {
			visit(children.Node(), nextParents)
		}
	}
	visit(function, nil)
	facts.ReadsGlobals = sortedIDs(reads)
	facts.WritesGlobals = sortedIDs(writes)
	facts.MutatedParameters = sortedIndexes(mutated)
	facts.Calls = sortedIDs(calls)
	facts.Parameters, facts.ReferenceParameters = orderedParameters(parameters)
	return facts
}

type parameterFact struct {
	index   int
	mutable bool
}

func functionCall(
	call parser.SyntaxNode,
	table *symbol.Table,
	references map[source.Span]symbol.Reference,
	spanOf func(parser.SyntaxNode) (source.Span, bool),
) (FunctionCall, bool) {
	callee, ok := call.Field("function")
	if !ok || callee.Kind() != parser.KindIdentifier {
		return FunctionCall{}, false
	}
	calleeSpan, mapped := spanOf(callee)
	reference, ok := references[calleeSpan]
	if !mapped || !ok || reference.Resolved == 0 {
		return FunctionCall{}, false
	}
	result := FunctionCall{Function: reference.Resolved}
	arguments, ok := call.Field("arguments")
	if !ok {
		return result, true
	}
	items := arguments.Children()
	for items.Next() {
		identifier, found := baseIdentifier(items.Node())
		if !found {
			result.Arguments = append(result.Arguments, 0)
			continue
		}
		argumentSpan, mapped := spanOf(identifier)
		argument, found := references[argumentSpan]
		if !mapped || !found {
			result.Arguments = append(result.Arguments, 0)
		} else {
			result.Arguments = append(result.Arguments, argument.Resolved)
		}
	}
	return result, true
}

func baseIdentifier(node parser.SyntaxNode) (parser.SyntaxNode, bool) {
	for node.Valid() {
		switch node.Kind() {
		case parser.KindIdentifier:
			return node, true
		case parser.KindParenthesizedExpression, parser.KindTaggedExpression:
			next, ok := node.Field("expression")
			if !ok {
				return parser.SyntaxNode{}, false
			}
			node = next
		case parser.KindSubscriptExpression:
			next, ok := node.Field("array")
			if !ok {
				return parser.SyntaxNode{}, false
			}
			node = next
		default:
			return parser.SyntaxNode{}, false
		}
	}
	return parser.SyntaxNode{}, false
}

func functionParameters(
	function parser.SyntaxNode,
	table *symbol.Table,
	spanOf func(parser.SyntaxNode) (source.Span, bool),
	hasReferenceMark func(parser.ByteRange) bool,
) map[symbol.ID]parameterFact {
	result := make(map[symbol.ID]parameterFact)
	list, ok := function.Field("parameters")
	if !ok {
		return result
	}
	index := 0
	parameters := list.Children()
	for parameters.Next() {
		parameter := parameters.Node()
		if parameter.Kind() != parser.KindParameter {
			continue
		}
		name, ok := parameter.Field("name")
		if ok {
			nameSpan, mapped := spanOf(name)
			if declared, found := table.DeclarationAt(nameSpan); mapped && found {
				result[declared.ID] = parameterFact{
					index: index, mutable: declared.IsArray || hasReferenceMark(parameter.Range()),
				}
			}
		}
		index++
	}
	return result
}

func orderedParameters(parameters map[symbol.ID]parameterFact) ([]symbol.ID, []bool) {
	ids := make([]symbol.ID, len(parameters))
	references := make([]bool, len(parameters))
	for id, parameter := range parameters {
		ids[parameter.index] = id
		references[parameter.index] = parameter.mutable
	}
	return ids, references
}

func applyReferenceFacts(
	facts *FunctionFacts,
	reference symbol.Reference,
	access referenceAccess,
	table *symbol.Table,
	parameters map[symbol.ID]parameterFact,
	reads, writes map[symbol.ID]struct{},
	mutated map[int]struct{},
	calls map[symbol.ID]struct{},
) {
	if reference.Resolved == 0 {
		if reference.IsCall {
			facts.Complete = false
		}
		return
	}
	resolved, ok := table.Symbol(reference.Resolved)
	if !ok {
		facts.Complete = false
		return
	}
	if reference.IsCall {
		calls[resolved.ID] = struct{}{}
	}
	if parameter, ok := parameters[resolved.ID]; ok && parameter.mutable && access != accessRead {
		mutated[parameter.index] = struct{}{}
	}
	scope, ok := table.Scope(resolved.Scope)
	if !ok || scope.Kind != symbol.ScopeFile || resolved.Kind.IsCallable() {
		return
	}
	if access == accessRead && resolved.IsConst {
		return
	}
	if access == accessRead {
		reads[resolved.ID] = struct{}{}
	} else {
		writes[resolved.ID] = struct{}{}
	}
}

type referenceAccess uint8

const (
	accessRead referenceAccess = iota
	accessWrite
	accessReadWrite
)

func accessFor(node parser.SyntaxNode, parents []parser.SyntaxNode) referenceAccess {
	current := node
	for index := len(parents) - 1; index >= 0; index-- {
		parent := parents[index]
		switch parent.Kind() {
		case parser.KindAssignmentExpression:
			left, ok := parent.Field("left")
			if ok && contains(left, current) {
				if parent.Token().Kind() == token.Assign {
					return accessWrite
				}
				return accessReadWrite
			}
			return accessRead
		case parser.KindUpdateExpression:
			return accessReadWrite
		case parser.KindSubscriptExpression, parser.KindParenthesizedExpression, parser.KindTaggedExpression:
			current = parent
		default:
			return accessRead
		}
	}
	return accessRead
}

func contains(container, node parser.SyntaxNode) bool {
	outer, inner := container.Range(), node.Range()
	return inner.Start >= outer.Start && inner.End <= outer.End
}

func scopeWithin(table *symbol.Table, scope, parent symbol.ID) bool {
	for scope != 0 {
		if scope == parent {
			return true
		}
		current, ok := table.Scope(scope)
		if !ok {
			return false
		}
		scope = current.Parent
	}
	return false
}

func compactRangeHasToken(tokens []parser.CompactToken, within parser.ByteRange, kind token.Kind) bool {
	for _, item := range tokens {
		if int(item.Start.Offset) >= within.Start && int(item.End.Offset) <= within.End && item.Kind == kind {
			return true
		}
	}
	return false
}

func tokenRangeHasToken(tokens []token.Token, within parser.ByteRange, kind token.Kind) bool {
	for _, item := range tokens {
		if item.Start.Offset >= within.Start && item.End.Offset <= within.End && item.Kind == kind {
			return true
		}
	}
	return false
}

func sortedIDs(values map[symbol.ID]struct{}) []symbol.ID {
	result := make([]symbol.ID, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func sortedIndexes(values map[int]struct{}) []int {
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}

func idSet(values []symbol.ID) map[symbol.ID]struct{} {
	result := make(map[symbol.ID]struct{}, len(values))
	addIDs(result, values)
	return result
}

func addIDs(target map[symbol.ID]struct{}, values []symbol.ID) {
	for _, value := range values {
		target[value] = struct{}{}
	}
}

func indexSet(values []int) map[int]struct{} {
	result := make(map[int]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func equalIDs(left, right []symbol.ID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalIndexes(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
