package sema

import (
	"context"
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
	entries map[symbol.StableID]cachedFlow
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
	flows, diagnostics, cache, reused, _ := checkControlFlowCached(
		context.Background(), false, root, table, previous,
	)
	return flows, diagnostics, cache, reused
}

// CheckControlFlowCachedContext reuses CFGs and observes cancellation.
func CheckControlFlowCachedContext(
	ctx context.Context,
	root parser.SyntaxNode,
	table *symbol.Table,
	previous *FlowCache,
) ([]FunctionFlow, []diagnostic.Diagnostic, *FlowCache, int, error) {
	return checkControlFlowCached(ctx, true, root, table, previous)
}

func checkControlFlowCached(
	ctx context.Context,
	cancellable bool,
	root parser.SyntaxNode,
	table *symbol.Table,
	previous *FlowCache,
) ([]FunctionFlow, []diagnostic.Diagnostic, *FlowCache, int, error) {
	cancel := cancellation{ctx: ctx, cancellable: cancellable}
	if cancellable {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, 0, err
		}
	}
	if !root.Valid() || table == nil {
		return nil, nil, &FlowCache{entries: make(map[symbol.StableID]cachedFlow)}, 0, nil
	}
	var flows []FunctionFlow
	var diagnostics []diagnostic.Diagnostic
	constants, err := ResolveConstantsContext(ctx, root, table)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	constantHash := constantFingerprint(table, constants)
	next := &FlowCache{entries: make(map[symbol.StableID]cachedFlow)}
	reused := 0
	decls := root.Declarations()
	for decls.Next() {
		if cancel.poll() {
			return nil, nil, nil, 0, cancel.err
		}
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
		var stableID symbol.StableID
		var stable bool
		var bodyHash [32]byte
		var graph *cfg.Graph
		if previous != nil && len(previous.entries) != 0 {
			stableID, stable = table.StableSymbolID(callable.ID)
		}
		if stable {
			if cached, ok := previous.entries[stableID]; ok {
				bodyHash = sha256.Sum256(body.Bytes())
				graph = reuseFlow(cached, source.Offset(body.Range().Start), bodyHash, constantHash, table.File)
				if graph != nil {
					reused++
				}
			}
		}
		if graph == nil {
			var err error
			graph, err = cfg.BuildWithEvaluatorContext(ctx, body, table.File, func(node parser.SyntaxNode) (int64, bool) {
				value := EvalConstantResolved(node, func(identifier parser.SyntaxNode) Constant {
					if item, ok := referencedSymbol(table, identifier); ok {
						return constants[item.ID]
					}
					return Constant{}
				})
				return value.Value, value.Known
			})
			if err != nil {
				return nil, nil, nil, 0, err
			}
		}
		if len(graph.Blocks) >= 5 {
			if !stable {
				stableID, stable = table.StableSymbolID(callable.ID)
			}
			if stable {
				if bodyHash == ([32]byte{}) {
					bodyHash = sha256.Sum256(body.Bytes())
				}
				next.entries[stableID] = cachedFlow{
					body: bodyHash, constants: constantHash, start: source.Offset(body.Range().Start), graph: graph,
				}
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
	return flows, diagnostics, next, reused, nil
}

func reuseFlow(
	cached cachedFlow,
	bodyStart source.Offset,
	body, constants [32]byte,
	file source.FileID,
) *cfg.Graph {
	if cached.body != body || cached.constants != constants {
		return nil
	}
	return shiftGraph(cached.graph, file, bodyStart-cached.start)
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
		stable symbol.StableID
		value  int64
	}
	entries := make([]entry, 0)
	for id, value := range values {
		item, ok := table.Symbol(id)
		if !ok || !value.Known {
			continue
		}
		stableID, stable := table.StableSymbolID(item.ID)
		if !stable {
			continue
		}
		entries = append(entries, entry{stable: stableID, value: value.Value})
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
