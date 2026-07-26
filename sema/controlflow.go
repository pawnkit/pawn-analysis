package sema

import (
	"fmt"

	"github.com/pawnkit/pawn-analysis/cfg"
	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/diagnostic"
)

type FunctionFlow struct {
	Symbol symbol.ID
	Graph  *cfg.Graph
}

// CheckControlFlow builds function CFGs and reports unreachable code and fallthrough.
func CheckControlFlow(root parser.SyntaxNode, table *symbol.Table) ([]FunctionFlow, []diagnostic.Diagnostic) {
	if !root.Valid() || table == nil {
		return nil, nil
	}
	var flows []FunctionFlow
	var diagnostics []diagnostic.Diagnostic
	constants := ResolveConstants(root, table)
	decls := root.Declarations()
	for decls.Next() {
		declaration := decls.Declaration()
		if declaration.Kind() != parser.KindFunctionDefinition {
			continue
		}
		name, nameOK := declaration.Field("name")
		body, bodyOK := declaration.Field("body")
		if !nameOK || !bodyOK {
			continue
		}
		callable, ok := declaredSymbol(table, name)
		if !ok {
			continue
		}
		graph := cfg.BuildWithEvaluator(body, table.File, func(node parser.SyntaxNode) (int64, bool) {
			value := EvalConstantResolved(node, func(identifier parser.SyntaxNode) Constant {
				if item, ok := referencedSymbol(table, identifier); ok {
					return constants[item.ID]
				}
				return Constant{}
			})
			return value.Value, value.Known
		})
		flows = append(flows, FunctionFlow{Symbol: callable.ID, Graph: graph})
		diagnostics = append(diagnostics, unreachableDiagnostics(graph)...)
		for _, jump := range graph.UnresolvedGotos {
			diagnostics = append(diagnostics, diagnostic.New(
				"pawn-analysis:sema/undefined-label", "pawn-analysis", diagnostic.SeverityError,
				fmt.Sprintf("undefined label %q", jump.Name), jump.Span,
			))
		}
		if callable.Tag != "" && callable.Tag != "void" && graph.FallsThrough {
			diagnostics = append(diagnostics, diagnostic.New(
				"pawn-analysis:sema/missing-return", "pawn-analysis", diagnostic.SeverityWarning,
				fmt.Sprintf("function %q can finish without returning %s", callable.Name, callable.Tag), callable.Span,
			))
		}
	}
	return flows, diagnostics
}

func unreachableDiagnostics(graph *cfg.Graph) []diagnostic.Diagnostic {
	reachable := graph.Reachable()
	predecessors := make(map[cfg.ID]int, len(graph.Blocks))
	for _, block := range graph.Blocks {
		for _, next := range block.Successors {
			predecessors[next]++
		}
	}
	var diagnostics []diagnostic.Diagnostic
	for _, block := range graph.Blocks {
		if reachable[block.ID] || predecessors[block.ID] != 0 {
			continue
		}
		diagnostics = append(diagnostics, diagnostic.New(
			"pawn-analysis:sema/unreachable", "pawn-analysis", diagnostic.SeverityWarning,
			"unreachable statement", block.Span,
		))
	}
	return diagnostics
}

func declaredSymbol(table *symbol.Table, node parser.SyntaxNode) (symbol.Symbol, bool) {
	return table.DeclarationAt(node.Range().Span(table.File))
}
