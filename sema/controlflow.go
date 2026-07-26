package sema

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/pawnkit/pawn-analysis/cfg"
	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

type FunctionFlow struct {
	Symbol symbol.ID
	Graph  *cfg.Graph
}

// FlowCache stores immutable function CFGs between document revisions.
type FlowCache struct {
	entries map[[32]byte]cachedFlow
}

type cachedFlow struct {
	body      [32]byte
	constants [32]byte
	start     source.Offset
	graph     *cfg.Graph
}

// CheckControlFlow builds function CFGs and reports unreachable code and fallthrough.
func CheckControlFlow(root parser.SyntaxNode, table *symbol.Table) ([]FunctionFlow, []diagnostic.Diagnostic) {
	flows, diagnostics, _, _ := CheckControlFlowCached(root, table, nil)
	return flows, diagnostics
}

// CheckControlFlowCached reuses CFGs for unchanged functions.
func CheckControlFlowCached(
	root parser.SyntaxNode,
	table *symbol.Table,
	previous *FlowCache,
) ([]FunctionFlow, []diagnostic.Diagnostic, *FlowCache, int) {
	if !root.Valid() || table == nil {
		return nil, nil, &FlowCache{entries: make(map[[32]byte]cachedFlow)}, 0
	}
	var flows []FunctionFlow
	var diagnostics []diagnostic.Diagnostic
	constants := ResolveConstants(root, table)
	constantHash := constantFingerprint(table, constants)
	next := &FlowCache{entries: make(map[[32]byte]cachedFlow)}
	reused := 0
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
		var bodyHash [32]byte
		var graph *cfg.Graph
		if previous != nil {
			if cached, ok := previous.entries[callable.StableID]; ok {
				bodyHash = sha256.Sum256(body.Bytes())
				graph = reuseFlow(cached, callable, bodyHash, constantHash, table.File)
				if graph != nil {
					reused++
				}
			}
		}
		if graph == nil {
			graph = cfg.BuildWithEvaluator(body, table.File, func(node parser.SyntaxNode) (int64, bool) {
				value := EvalConstantResolved(node, func(identifier parser.SyntaxNode) Constant {
					if item, ok := referencedSymbol(table, identifier); ok {
						return constants[item.ID]
					}
					return Constant{}
				})
				return value.Value, value.Known
			})
		}
		if len(graph.Blocks) >= 5 {
			if bodyHash == ([32]byte{}) {
				bodyHash = sha256.Sum256(body.Bytes())
			}
			next.entries[callable.StableID] = cachedFlow{
				body: bodyHash, constants: constantHash, start: callable.Span.Start, graph: graph,
			}
		}
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
	return flows, diagnostics, next, reused
}

func reuseFlow(
	cached cachedFlow,
	callable symbol.Symbol,
	body, constants [32]byte,
	file source.FileID,
) *cfg.Graph {
	if callable.StableID == ([32]byte{}) {
		return nil
	}
	if cached.body != body || cached.constants != constants {
		return nil
	}
	return shiftGraph(cached.graph, file, callable.Span.Start-cached.start)
}

func shiftGraph(graph *cfg.Graph, file source.FileID, delta source.Offset) *cfg.Graph {
	if graph == nil {
		return nil
	}
	if delta == 0 {
		return graph
	}
	result := *graph
	result.Blocks = append([]cfg.Block(nil), graph.Blocks...)
	for i := range result.Blocks {
		result.Blocks[i].Span.File = file
		result.Blocks[i].Span.Start += delta
		result.Blocks[i].Span.End += delta
		result.Blocks[i].Successors = append([]cfg.ID(nil), graph.Blocks[i].Successors...)
	}
	result.UnresolvedGotos = append([]cfg.Goto(nil), graph.UnresolvedGotos...)
	for i := range result.UnresolvedGotos {
		result.UnresolvedGotos[i].Span.File = file
		result.UnresolvedGotos[i].Span.Start += delta
		result.UnresolvedGotos[i].Span.End += delta
	}
	return &result
}

func constantFingerprint(table *symbol.Table, values map[symbol.ID]Constant) [32]byte {
	type entry struct {
		stable [32]byte
		value  int64
	}
	entries := make([]entry, 0)
	for id, value := range values {
		item, ok := table.Symbol(id)
		if !ok || item.StableID == ([32]byte{}) || !value.Known {
			continue
		}
		entries = append(entries, entry{stable: item.StableID, value: value.Value})
	}
	sort.Slice(entries, func(i, j int) bool {
		return string(entries[i].stable[:]) < string(entries[j].stable[:])
	})
	hash := sha256.New()
	var encoded [8]byte
	for _, item := range entries {
		hash.Write(item.stable[:])
		binary.LittleEndian.PutUint64(encoded[:], uint64(item.value))
		hash.Write(encoded[:])
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
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
