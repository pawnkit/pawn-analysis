package sema_test

import (
	"reflect"
	"testing"

	"github.com/pawnkit/pawn-analysis/sema"
	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/source"
)

func TestBuildFunctionFacts(t *testing.T) {
	t.Parallel()

	text := []byte(`
new input;
new output;

stock Mutate(&value, values[]) {
    output = input;
    value++;
    values[0] = output;
}

stock Run() {
    new local;
    Mutate(local, output);
}
`)
	file := parser.ParseCompact(text, parser.ParseOptions{})
	table := symbol.Build(file.Syntax(), source.FileID(1))
	facts := sema.BuildFunctionFacts(file, table)

	mutate := symbolByName(t, table, "Mutate")
	input := symbolByName(t, table, "input")
	output := symbolByName(t, table, "output")
	got := facts[mutate.ID]
	if !got.Complete || got.IntrinsicImpure {
		t.Fatalf("Mutate completeness = %v, intrinsic impurity = %v", got.Complete, got.IntrinsicImpure)
	}
	if !reflect.DeepEqual(got.ReadsGlobals, []symbol.ID{input.ID, output.ID}) {
		t.Fatalf("global reads = %v", got.ReadsGlobals)
	}
	if !reflect.DeepEqual(got.WritesGlobals, []symbol.ID{output.ID}) {
		t.Fatalf("global writes = %v", got.WritesGlobals)
	}
	if !reflect.DeepEqual(got.MutatedParameters, []int{0, 1}) {
		t.Fatalf("mutated parameters = %v", got.MutatedParameters)
	}

	run := symbolByName(t, table, "Run")
	if !reflect.DeepEqual(facts[run.ID].Calls, []symbol.ID{mutate.ID}) {
		t.Fatalf("Run calls = %v", facts[run.ID].Calls)
	}
}

func TestBuildFunctionFactsMarksUnknownCallsIncomplete(t *testing.T) {
	t.Parallel()

	text := []byte("stock Run() { Missing(); }\n")
	file := parser.ParseCompact(text, parser.ParseOptions{})
	table := symbol.Build(file.Syntax(), source.FileID(1))
	run := symbolByName(t, table, "Run")
	facts := sema.ResolveFunctionFacts(sema.BuildFunctionFacts(file, table), table)
	if facts[run.ID].Complete {
		t.Fatal("unresolved call produced complete function facts")
	}
}

func TestBuildFunctionFactsMarksStaticLocalsImpure(t *testing.T) {
	t.Parallel()

	text := []byte(`
stock Cached() { static value; return value; }
stock Plain() { new value; return value; }
`)
	file := parser.ParseCompact(text, parser.ParseOptions{})
	table := symbol.Build(file.Syntax(), source.FileID(1))
	facts := sema.BuildFunctionFacts(file, table)
	if !facts[symbolByName(t, table, "Cached").ID].IntrinsicImpure {
		t.Fatal("static local did not mark function impure")
	}
	if facts[symbolByName(t, table, "Plain").ID].IntrinsicImpure {
		t.Fatal("ordinary local marked function impure")
	}
}

func TestResolveFunctionFactsUsesExternalEffects(t *testing.T) {
	t.Parallel()

	text := []byte(`
new shared;
stock Forward(&value) { External(value); }
stock WriteGlobal() { Forward(shared); }
`)
	file := parser.ParseCompact(text, parser.ParseOptions{})
	table := symbol.Build(file.Syntax(), source.FileID(1))
	facts := sema.ResolveFunctionFactsWithResolver(
		sema.BuildFunctionFacts(file, table),
		table,
		callEffectResolver{
			"External": {
				Complete: true, IntrinsicImpure: true, MutatedParameters: []int{0},
			},
		},
	)

	forward := facts[symbolByName(t, table, "Forward").ID]
	if !forward.Complete || !forward.IntrinsicImpure ||
		!reflect.DeepEqual(forward.MutatedParameters, []int{0}) {
		t.Fatalf("Forward facts = %#v", forward)
	}
	writeGlobal := facts[symbolByName(t, table, "WriteGlobal").ID]
	if !writeGlobal.Complete || !writeGlobal.IntrinsicImpure ||
		len(writeGlobal.WritesGlobals) != 1 {
		t.Fatalf("WriteGlobal facts = %#v", writeGlobal)
	}
}

func TestBuildFunctionFactsIncludesCallableDeclarations(t *testing.T) {
	t.Parallel()

	text := []byte(`
native External(input, output[], const stable[]);
stock Forward(input, output[], const stable[]) {
    External(input, output, stable);
}
`)
	file := parser.ParseCompact(text, parser.ParseOptions{})
	table := symbol.Build(file.Syntax(), source.FileID(1))
	facts := sema.ResolveFunctionFacts(sema.BuildFunctionFacts(file, table), table)

	external := facts[symbolByName(t, table, "External").ID]
	if !external.Complete || !external.IntrinsicImpure ||
		!reflect.DeepEqual(external.MutatedParameters, []int{1}) {
		t.Fatalf("External facts = %#v", external)
	}
	forward := facts[symbolByName(t, table, "Forward").ID]
	if !forward.Complete || !forward.IntrinsicImpure ||
		!reflect.DeepEqual(forward.MutatedParameters, []int{1}) {
		t.Fatalf("Forward facts = %#v", forward)
	}
}

type callEffectResolver map[string]sema.CallEffects

func (r callEffectResolver) ResolveCallEffects(name string) (sema.CallEffects, bool) {
	effects, ok := r[name]
	return effects, ok
}

func TestResolveFunctionFactsPropagatesCalls(t *testing.T) {
	t.Parallel()

	text := []byte(`
new shared;
stock Mutate(&value) { value = 1; shared = 2; }
stock Forward(&value) { Mutate(value); }
stock WriteGlobal() { Forward(shared); }
`)
	file := parser.ParseCompact(text, parser.ParseOptions{})
	table := symbol.Build(file.Syntax(), source.FileID(1))
	facts := sema.ResolveFunctionFacts(sema.BuildFunctionFacts(file, table), table)

	forward := facts[symbolByName(t, table, "Forward").ID]
	if !forward.Complete || !reflect.DeepEqual(forward.MutatedParameters, []int{0}) ||
		len(forward.WritesGlobals) != 1 {
		t.Fatalf("Forward facts = %#v", forward)
	}
	writeGlobal := facts[symbolByName(t, table, "WriteGlobal").ID]
	if !writeGlobal.Complete || len(writeGlobal.MutatedParameters) != 0 ||
		len(writeGlobal.WritesGlobals) != 1 {
		t.Fatalf("WriteGlobal facts = %#v", writeGlobal)
	}
}

func symbolByName(t *testing.T, table *symbol.Table, name string) symbol.Symbol {
	t.Helper()
	for _, item := range table.Symbols {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("symbol %q not found", name)
	return symbol.Symbol{}
}
