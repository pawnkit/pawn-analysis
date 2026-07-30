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
	Complete          bool
	IntrinsicImpure   bool
	ReadsGlobals      []symbol.ID
	WritesGlobals     []symbol.ID
	MutatedParameters []int
	Calls             []symbol.ID
}

// BuildFunctionFacts collects direct effects without following calls.
func BuildFunctionFacts(file *parser.CompactFile, table *symbol.Table) map[symbol.ID]FunctionFacts {
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
		declared, ok := table.DeclarationAt(name.Range().Span(table.File))
		if !ok || !declared.Kind.IsCallable() {
			continue
		}
		result[declared.ID] = collectFunctionFacts(file, function, declared, table, references)
	}
	return result
}

func collectFunctionFacts(
	file *parser.CompactFile,
	function parser.SyntaxNode,
	declared symbol.Symbol,
	table *symbol.Table,
	references map[source.Span]symbol.Reference,
) FunctionFacts {
	facts := FunctionFacts{Complete: !function.HasError()}
	parameters := functionParameters(function, file.Tokens, table)
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
		case parser.KindIdentifier:
			reference, ok := references[node.Range().Span(table.File)]
			if ok {
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
	return facts
}

type parameterFact struct {
	index   int
	mutable bool
}

func functionParameters(
	function parser.SyntaxNode,
	tokens []parser.CompactToken,
	table *symbol.Table,
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
			if declared, found := table.DeclarationAt(name.Range().Span(table.File)); found {
				result[declared.ID] = parameterFact{
					index: index, mutable: declared.IsArray || rangeHasToken(tokens, parameter.Range(), token.Amp),
				}
			}
		}
		index++
	}
	return result
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

func rangeHasToken(tokens []parser.CompactToken, within parser.ByteRange, kind token.Kind) bool {
	for _, item := range tokens {
		if int(item.Start.Offset) >= within.Start && int(item.End.Offset) <= within.End && item.Kind == kind {
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
