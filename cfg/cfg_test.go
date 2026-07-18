package cfg_test

import (
	"testing"

	"github.com/pawnkit/pawn-analysis/cfg"
	"github.com/pawnkit/pawn-analysis/sema"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/source"
)

func graphFor(t *testing.T, text string) *cfg.Graph {
	t.Helper()
	file := parser.ParseCompact([]byte(text), parser.ParseOptions{})
	root := file.Syntax()
	decls := root.Declarations()
	if !decls.Next() {
		t.Fatal("function missing")
	}
	body, ok := decls.Declaration().Field("body")
	if !ok {
		t.Fatal("body missing")
	}
	return cfg.BuildWithEvaluator(body, source.FileID(1), func(node parser.SyntaxNode) (int64, bool) {
		value := sema.EvalConstant(node)
		return value.Value, value.Known
	})
}

func TestReturnTerminatesFlow(t *testing.T) {
	graph := graphFor(t, "main() { return 1; new value; }")
	reachable := graph.Reachable()
	if len(graph.Blocks) != 2 || !reachable[1] || reachable[2] || graph.FallsThrough {
		t.Fatalf("blocks=%+v reachable=%v fallthrough=%v", graph.Blocks, reachable, graph.FallsThrough)
	}
}

func TestIfBothBranchesReturn(t *testing.T) {
	graph := graphFor(t, "main() { if (value) return 1; else return 2; }")
	if graph.FallsThrough {
		t.Fatal("fully returning branch falls through")
	}
}

func TestConstantIfPrunesImpossibleBranch(t *testing.T) {
	graph := graphFor(t, "main() { if (0) return 1; else return 2; }")
	reachable := graph.Reachable()
	if len(graph.Blocks) != 3 || !reachable[1] || reachable[2] || !reachable[3] {
		t.Fatalf("blocks=%+v reachable=%v", graph.Blocks, reachable)
	}
}

func TestLoopCanFallThrough(t *testing.T) {
	graph := graphFor(t, "main() { while (value) { value--; } }")
	if !graph.FallsThrough {
		t.Fatal("loop must retain a false-condition exit")
	}
}

func TestConstantFalseLoopBodyIsUnreachable(t *testing.T) {
	graph := graphFor(t, "main() { while (0) { value++; } return 1; }")
	reachable := graph.Reachable()
	if len(graph.Blocks) != 3 || !reachable[1] || reachable[2] || !reachable[3] {
		t.Fatalf("blocks=%+v reachable=%v", graph.Blocks, reachable)
	}
}

func TestConstantTrueLoopDoesNotFallThrough(t *testing.T) {
	graph := graphFor(t, "main() { while (1) { value++; } }")
	if graph.FallsThrough {
		t.Fatal("constant true loop without break must not fall through")
	}
}

func TestConstantFalseDoWhileRunsBodyOnce(t *testing.T) {
	graph := graphFor(t, "main() { do { return 1; } while (0); }")
	reachable := graph.Reachable()
	if len(graph.Blocks) != 2 || !reachable[1] || !reachable[2] || graph.FallsThrough {
		t.Fatalf("blocks=%+v reachable=%v fallthrough=%v", graph.Blocks, reachable, graph.FallsThrough)
	}
}

func TestBreakExitsLoop(t *testing.T) {
	graph := graphFor(t, "main() { while (value) { break; new skipped; } new reached; }")
	reachable := graph.Reachable()
	if len(graph.Blocks) != 4 || !reachable[1] || !reachable[2] || reachable[3] || !reachable[4] {
		t.Fatalf("blocks=%+v reachable=%v", graph.Blocks, reachable)
	}
	if graph.FallsThrough == false {
		t.Fatal("statement after loop must fall through")
	}
}

func TestContinueReturnsToLoopHeader(t *testing.T) {
	graph := graphFor(t, "main() { while (value) { continue; new skipped; } new reached; }")
	reachable := graph.Reachable()
	if len(graph.Blocks) != 4 || !reachable[1] || !reachable[2] || reachable[3] || !reachable[4] {
		t.Fatalf("blocks=%+v reachable=%v", graph.Blocks, reachable)
	}
	foundBackEdge := false
	for _, successor := range graph.Blocks[1].Successors {
		if successor == graph.Entry {
			foundBackEdge = true
		}
	}
	if !foundBackEdge {
		t.Fatalf("continue successors=%v, want loop header %d", graph.Blocks[1].Successors, graph.Entry)
	}
}

func TestSwitchWithoutDefaultCanFallThrough(t *testing.T) {
	graph := graphFor(t, "main() { switch (value) { case 1: return 1; } }")
	if !graph.FallsThrough {
		t.Fatal("switch without default has an unmatched path")
	}
}

func TestReturningSwitchWithDefaultDoesNotFallThrough(t *testing.T) {
	graph := graphFor(t, "main() { switch (value) { case 1: return 1; default: return 0; } }")
	if graph.FallsThrough {
		t.Fatal("switch with returning case and default must not fall through")
	}
}

func TestBreakExitsSwitch(t *testing.T) {
	graph := graphFor(t, "main() { switch (value) { case 1: { break; new skipped; } } new reached; }")
	reachable := graph.Reachable()
	if len(graph.Blocks) != 4 || !reachable[1] || !reachable[2] || reachable[3] || !reachable[4] {
		t.Fatalf("blocks=%+v reachable=%v", graph.Blocks, reachable)
	}
}

func TestContinueInsideSwitchReturnsToLoop(t *testing.T) {
	graph := graphFor(t, "main() { while (value) { switch (value) { case 1: continue; } new reached; } }")
	reachable := graph.Reachable()
	if len(graph.Blocks) != 4 || !reachable[1] || !reachable[2] || !reachable[3] || !reachable[4] {
		t.Fatalf("blocks=%+v reachable=%v", graph.Blocks, reachable)
	}
	foundBackEdge := false
	for _, block := range graph.Blocks {
		if block.Kind != parser.KindContinueStatement {
			continue
		}
		for _, successor := range block.Successors {
			if successor == graph.Entry {
				foundBackEdge = true
			}
		}
	}
	if !foundBackEdge {
		t.Fatal("continue inside switch does not return to loop header")
	}
}

func TestForwardGotoResolvesLabel(t *testing.T) {
	graph := graphFor(t, "main() { goto done; new skipped; done: return 1; }")
	reachable := graph.Reachable()
	if len(graph.Blocks) != 4 || !reachable[1] || reachable[2] || !reachable[3] || !reachable[4] {
		t.Fatalf("blocks=%+v reachable=%v", graph.Blocks, reachable)
	}
	if len(graph.UnresolvedGotos) != 0 || graph.FallsThrough {
		t.Fatalf("unresolved=%+v fallthrough=%v", graph.UnresolvedGotos, graph.FallsThrough)
	}
}

func TestBackwardGotoResolvesLabel(t *testing.T) {
	graph := graphFor(t, "main() { again: value++; goto again; }")
	if len(graph.UnresolvedGotos) != 0 || graph.FallsThrough {
		t.Fatalf("unresolved=%+v fallthrough=%v", graph.UnresolvedGotos, graph.FallsThrough)
	}
	gotos := graph.Blocks[len(graph.Blocks)-1]
	if len(gotos.Successors) != 1 || gotos.Successors[0] != graph.Entry {
		t.Fatalf("goto successors=%v, entry=%d", gotos.Successors, graph.Entry)
	}
}

func TestMissingGotoLabelIsRecorded(t *testing.T) {
	graph := graphFor(t, "main() { goto missing; }")
	if len(graph.UnresolvedGotos) != 1 || graph.UnresolvedGotos[0].Name != "missing" {
		t.Fatalf("unresolved=%+v", graph.UnresolvedGotos)
	}
}
