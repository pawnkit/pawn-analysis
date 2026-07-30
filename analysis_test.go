package analysis_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	analysis "github.com/pawnkit/pawn-analysis"
	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawn-analysis/sema"
	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/source"
)

type callableResolver map[string]sema.Callable

func (r callableResolver) ResolveName(name string) sema.NameState {
	if _, ok := r[name]; ok {
		return sema.NameFound
	}
	return sema.NameUnknown
}

func (r callableResolver) ResolveCallable(name string) (sema.Callable, bool) {
	callable, ok := r[name]
	return callable, ok
}

func (r callableResolver) ResolveCallEffects(name string) (sema.CallEffects, bool) {
	_, ok := r[name]
	if !ok {
		return sema.CallEffects{}, false
	}
	return sema.CallEffects{Complete: true, IntrinsicImpure: true}, true
}

type externalEffectResolver struct {
	callableResolver
	effects map[string]sema.CallEffects
}

func (r externalEffectResolver) ResolveCallEffects(name string) (sema.CallEffects, bool) {
	effects, ok := r.effects[name]
	return effects, ok
}

func TestAnalyzeContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := analysis.AnalyzeContext(ctx, []byte("main() {}"), analysis.Options{})
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("AnalyzeContext() = (%v, %v), want cancellation", result, err)
	}
}

func TestAnalyzeContextStopsDuringParsing(t *testing.T) {
	ctx := &cancelAfterChecksContext{}
	ctx.remaining.Store(3)
	result, err := analysis.AnalyzeContext(ctx, syntheticGlobalGamemode(5000), analysis.Options{})
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("AnalyzeContext() = (%v, %v), want cancellation", result, err)
	}
}

type cancelAfterChecksContext struct {
	remaining atomic.Int32
}

func (c *cancelAfterChecksContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfterChecksContext) Done() <-chan struct{}       { return nil }
func (c *cancelAfterChecksContext) Value(any) any               { return nil }

func (c *cancelAfterChecksContext) Err() error {
	if c.remaining.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

func TestAnalyzeTraceReportsStages(t *testing.T) {
	var stages []analysis.Stage
	result, err := analysis.AnalyzeContext(context.Background(), []byte("main() { return 1; }"), analysis.Options{
		RetainExpanded: true,
		Trace: func(event analysis.TraceEvent) {
			stages = append(stages, event.Stage)
			if event.Duration < 0 {
				t.Errorf("%s duration = %s", event.Stage, event.Duration)
			}
		},
	})
	if err != nil || result == nil {
		t.Fatalf("AnalyzeContext() = (%v, %v)", result, err)
	}
	want := []analysis.Stage{
		analysis.StagePreprocess,
		analysis.StageParseOriginal,
		analysis.StageParseExpanded,
		analysis.StageSymbolsOriginal,
		analysis.StageSymbolsExpanded,
		analysis.StageDeclarations,
		analysis.StageSemanticNames,
		analysis.StageSemanticTags,
		analysis.StageSemanticStates,
		analysis.StageSemanticOrder,
		analysis.StageSemanticCFG,
	}
	if !reflect.DeepEqual(stages, want) {
		t.Fatalf("stages = %v, want %v", stages, want)
	}
}

func TestAnalyzeContextSkipsSemanticStagesAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var stages []analysis.Stage
	result, err := analysis.AnalyzeContext(ctx, []byte("main() { return 1; }"), analysis.Options{
		Trace: func(event analysis.TraceEvent) {
			stages = append(stages, event.Stage)
			if event.Stage == analysis.StageSemanticNames {
				cancel()
			}
		},
	})
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("AnalyzeContext() = (%v, %v), want cancellation", result, err)
	}
	for _, stage := range stages {
		if stage == analysis.StageSemanticTags {
			t.Fatal("tag analysis ran after cancellation")
		}
	}
}

func TestAnalyzeTraceSerializesParallelStages(t *testing.T) {
	counts := make(map[analysis.Stage]int)
	result, err := analysis.AnalyzeContext(context.Background(), syntheticGlobalGamemode(2000), analysis.Options{
		RetainExpanded: true,
		Trace: func(event analysis.TraceEvent) {
			counts[event.Stage]++
		},
	})
	if err != nil || result == nil {
		t.Fatalf("AnalyzeContext() = (%v, %v)", result, err)
	}
	for _, stage := range []analysis.Stage{
		analysis.StageParseOriginal,
		analysis.StageSymbolsOriginal,
		analysis.StageParseExpanded,
		analysis.StageSymbolsExpanded,
	} {
		if counts[stage] != 1 {
			t.Errorf("%s count = %d, want 1", stage, counts[stage])
		}
	}
}

func TestCompleteContextMatchesCleanAnalysis(t *testing.T) {
	text := []byte("stock Helper(value) { return value; }\nmain() { Helper(1); }\n")
	opts := analysis.Options{URI: source.FileURI("test.pwn"), RetainExpanded: true}
	prepared, err := analysis.AnalyzeContext(context.Background(), text, analysis.Options{
		URI: opts.URI, RetainExpanded: true, SkipSemantics: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := analysis.CompleteContext(context.Background(), prepared, opts)
	if err != nil {
		t.Fatal(err)
	}
	want, err := analysis.AnalyzeContext(context.Background(), text, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Diagnostics, want.Diagnostics) {
		t.Fatalf("diagnostics differ:\ngot  %#v\nwant %#v", got.Diagnostics, want.Diagnostics)
	}
	if got.Parse != prepared.Parse || got.Symbols != prepared.Symbols {
		t.Fatal("completion did not reuse prepared syntax and symbols")
	}
}

func TestAnalyzePipeline(t *testing.T) {
	result := analysis.Analyze([]byte("#define TARGET Helper\nmain() { TARGET(); }\nHelper() {}\n"), analysis.Options{
		URI: source.FileURI("test.pwn"), Names: sema.MapResolver{}, RetainExpanded: true,
	})
	if result.Parse == nil || result.ExpandedParse == nil || result.Symbols == nil || result.ExpandedSymbols == nil || result.Preprocess == nil {
		t.Fatal("pipeline returned an incomplete result")
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", result.Diagnostics)
	}
}

func TestAnalyzeTracksUnchangedDeclarations(t *testing.T) {
	before := analysis.Analyze([]byte("stock Keep() { return 1; }\nstock Edit() { return 1; }\n"), analysis.Options{})
	after := analysis.Analyze(
		[]byte("stock Keep() { return 1; }\nstock Edit() { return 2; }\n"),
		analysis.Options{Previous: before},
	)
	if !after.Declarations.Reliable() {
		t.Fatal("declaration boundaries are not reliable")
	}
	if after.Reuse.Declarations != 1 {
		t.Fatalf("reused declarations = %d, want 1", after.Reuse.Declarations)
	}
}

func TestAnalyzeReusesExpandedViewForEquivalentTriviaEdit(t *testing.T) {
	before := analysis.Analyze(
		[]byte("stock Work() { return 1; } // old\n"),
		analysis.Options{RetainExpanded: true, Revision: "project:1"},
	)
	var reusedPreprocess, reusedParse, reusedSymbols int
	after, err := analysis.AnalyzeContext(
		context.Background(),
		[]byte("stock Work() { return 1; } // new\n"),
		analysis.Options{
			RetainExpanded: true,
			Previous:       before,
			Revision:       "project:1",
			Trace: func(event analysis.TraceEvent) {
				switch event.Stage {
				case analysis.StagePreprocess:
					reusedPreprocess = event.Reused
				case analysis.StageParseExpanded:
					reusedParse = event.Reused
				case analysis.StageSymbolsExpanded:
					reusedSymbols = event.Reused
				}
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if after.ExpandedParse != before.ExpandedParse || after.ExpandedSymbols != before.ExpandedSymbols {
		t.Fatal("expanded view was rebuilt")
	}
	if reusedPreprocess != 1 || reusedParse != 1 || reusedSymbols != 1 {
		t.Fatalf(
			"reuse trace = preprocess %d, parse %d, symbols %d",
			reusedPreprocess, reusedParse, reusedSymbols,
		)
	}
}

func TestAnalyzeReusesExpandedViewForLocalTokenEdit(t *testing.T) {
	includes := preprocess.MapResolver{
		"shared": bytes.Repeat([]byte("stock Included() {}\n"), 20),
	}
	before := analysis.Analyze(
		[]byte("#include <shared>\nstock Work() { return 1; }\nstock Keep() { return 2; }\n"),
		analysis.Options{Includes: includes, RetainExpanded: true, Revision: "project:1"},
	)
	var reusedOriginal, reusedSymbols int
	reusedSemantics := make(map[analysis.Stage]int)
	after, err := analysis.AnalyzeContext(
		context.Background(),
		[]byte("#include <shared>\nstock Work() { return 3; }\nstock Keep() { return 2; }\n"),
		analysis.Options{
			Includes: includes, RetainExpanded: true, Previous: before, Revision: "project:1",
			ReuseCompatibleExpansion: true,
			Trace: func(event analysis.TraceEvent) {
				if event.Stage == analysis.StageParseOriginal {
					reusedOriginal = event.Reused
				}
				if event.Stage == analysis.StageSymbolsOriginal {
					reusedSymbols = event.Reused
				}
				switch event.Stage {
				case analysis.StageSemanticNames, analysis.StageSemanticStates, analysis.StageSemanticOrder:
					reusedSemantics[event.Stage] = event.Reused
				}
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if after.ExpandedParse != before.ExpandedParse || after.ExpandedSymbols != before.ExpandedSymbols {
		t.Fatal("expanded view was rebuilt for a local token edit")
	}
	if reusedOriginal != 1 {
		t.Fatal("original syntax was rebuilt for a layout-compatible edit")
	}
	if &after.Parse.Tree.Nodes[0] != &before.Parse.Tree.Nodes[0] {
		t.Fatal("original syntax storage was not reused")
	}
	if reusedSymbols != 1 || after.Symbols != before.Symbols {
		t.Fatal("unchanged original symbols were rebuilt")
	}
	for _, stage := range []analysis.Stage{
		analysis.StageSemanticNames,
		analysis.StageSemanticStates,
		analysis.StageSemanticOrder,
	} {
		if reusedSemantics[stage] != 1 {
			t.Fatalf("%s was rebuilt", stage)
		}
	}
	if after.Reuse.Declarations == 0 {
		t.Fatal("unchanged declaration was not reused")
	}
	clean := analysis.Analyze(
		[]byte("#include <shared>\nstock Work() { return 3; }\nstock Keep() { return 2; }\n"),
		analysis.Options{Includes: includes, RetainExpanded: true, Revision: "project:1"},
	)
	if !reflect.DeepEqual(after.Diagnostics, clean.Diagnostics) {
		t.Fatalf("incremental diagnostics differ:\ngot  %#v\nwant %#v", after.Diagnostics, clean.Diagnostics)
	}
}

func TestAnalyzeRebasesOriginalSyntaxForTriviaEdit(t *testing.T) {
	includes := preprocess.MapResolver{
		"shared": bytes.Repeat([]byte("stock Included() {}\n"), 20),
	}
	before := analysis.Analyze(
		[]byte("#include <shared>\nstock Work() { return 1; }\nstock Keep() { return 2; }\n"),
		analysis.Options{Includes: includes, RetainExpanded: true, Revision: "project:1"},
	)
	var reusedParse, reusedSymbols int
	after, err := analysis.AnalyzeContext(
		context.Background(),
		[]byte("#include <shared>\nstock Work() {    return 1; }\nstock Keep() { return 2; }\n"),
		analysis.Options{
			Includes: includes, RetainExpanded: true, Previous: before, Revision: "project:1",
			ReuseCompatibleExpansion: true,
			Trace: func(event analysis.TraceEvent) {
				switch event.Stage {
				case analysis.StageParseOriginal:
					reusedParse = event.Reused
				case analysis.StageSymbolsOriginal:
					reusedSymbols = event.Reused
				}
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if reusedParse != 1 {
		t.Fatal("original syntax was rebuilt after a trivia edit")
	}
	if reusedSymbols != 0 || after.Symbols == before.Symbols {
		t.Fatal("shifted original symbols were reused")
	}
	if !after.Reuse.RebasedSyntax {
		t.Fatal("rebased syntax was not reported")
	}
	clean := analysis.Analyze(
		[]byte("#include <shared>\nstock Work() {    return 1; }\nstock Keep() { return 2; }\n"),
		analysis.Options{Includes: includes, RetainExpanded: true, Revision: "project:1"},
	)
	if !reflect.DeepEqual(after.Diagnostics, clean.Diagnostics) ||
		!reflect.DeepEqual(after.Declarations, clean.Declarations) {
		t.Fatal("rebased analysis differs from clean analysis")
	}
}

func TestAnalyzeReparsesChangedDeclaration(t *testing.T) {
	const revision = "project:1"
	beforeText := []byte("stock Work() { return value; }\nstock Keep() { return 2; }\n")
	afterText := []byte("stock Work() { return (value); }\nstock Keep() { return 2; }\n")
	before := analysis.Analyze(beforeText, analysis.Options{Revision: revision})
	var reusedParse, reusedSymbols int
	after := analysis.Analyze(afterText, analysis.Options{
		Previous: before, Revision: revision, ReuseCompatibleExpansion: true,
		Trace: func(event analysis.TraceEvent) {
			if event.Stage == analysis.StageParseOriginal {
				reusedParse = event.Reused
			}
			if event.Stage == analysis.StageSymbolsOriginal {
				reusedSymbols = event.Reused
			}
		},
	})
	if reusedParse != 1 || reusedSymbols != 1 || !after.Reuse.ReparsedDeclaration {
		t.Fatal("changed declaration was fully reparsed")
	}
	clean := analysis.Analyze(afterText, analysis.Options{Revision: revision})
	if !reflect.DeepEqual(after.Diagnostics, clean.Diagnostics) ||
		!reflect.DeepEqual(after.Declarations, clean.Declarations) {
		t.Fatal("incremental declaration analysis differs from clean analysis")
	}
}

func TestAnalyzeCollectsFunctionFactsOnRequest(t *testing.T) {
	text := []byte("new value;\nstock Read() { return value; }\n")
	without := analysis.Analyze(text, analysis.Options{})
	if without.FunctionFacts != nil {
		t.Fatal("default analysis collected function facts")
	}
	with := analysis.Analyze(text, analysis.Options{CollectFunctionFacts: true})
	var function symbol.ID
	for _, item := range with.Symbols.Symbols {
		if item.Name == "Read" {
			function = item.ID
			break
		}
	}
	if function == 0 || len(with.FunctionFacts[function].ReadsGlobals) != 1 {
		t.Fatal("requested function facts were not collected")
	}
}

func TestAnalyzeCollectsFunctionFactsAcrossIncludes(t *testing.T) {
	includes := preprocess.MapResolver{
		"shared": []byte("stock Mutate(&value) { value = 1; }\n"),
	}
	result := analysis.Analyze(
		[]byte("#include <shared>\nstock Forward(&value) { Mutate(value); }\n"),
		analysis.Options{
			URI: source.FileURI("main.pwn"), Includes: includes, RetainExpanded: true,
			CollectFunctionFacts: true,
		},
	)
	var forward symbol.ID
	for _, item := range result.ExpandedSymbols.Symbols {
		if item.Name == "Forward" {
			forward = item.ID
			break
		}
	}
	facts, ok := result.FunctionFacts[forward]
	if forward == 0 || !ok || !facts.Complete ||
		!reflect.DeepEqual(facts.MutatedParameters, []int{0}) {
		t.Fatalf("included function facts = %#v, found = %v", facts, ok)
	}
}

func TestAnalyzeCollectsExternalFunctionEffects(t *testing.T) {
	result := analysis.Analyze(
		[]byte("stock Forward(&value) { External(value); }\n"),
		analysis.Options{
			Names: externalEffectResolver{
				callableResolver: callableResolver{"External": {}},
				effects: map[string]sema.CallEffects{
					"External": {
						Complete: true, IntrinsicImpure: true, MutatedParameters: []int{0},
					},
				},
			},
			CollectFunctionFacts: true,
		},
	)
	var forward symbol.ID
	for _, item := range result.Symbols.Symbols {
		if item.Name == "Forward" {
			forward = item.ID
			break
		}
	}
	facts := result.FunctionFacts[forward]
	if forward == 0 || !facts.Complete || !facts.IntrinsicImpure ||
		!reflect.DeepEqual(facts.MutatedParameters, []int{0}) {
		t.Fatalf("external function facts = %#v", facts)
	}
}

func TestAnalyzeReusedSyntaxReadsCurrentSource(t *testing.T) {
	const revision = "project:1"
	before := analysis.Analyze(
		[]byte("stock Work() { new old = 1; return old; }\n"),
		analysis.Options{Revision: revision},
	)
	after := analysis.Analyze(
		[]byte("stock Work() { new now = 1; return now; }\n"),
		analysis.Options{
			Previous: before, Revision: revision, ReuseCompatibleExpansion: true,
		},
	)
	var foundNow, foundOld bool
	for _, item := range after.Symbols.Symbols {
		foundNow = foundNow || item.Name == "now"
		foundOld = foundOld || item.Name == "old"
	}
	if !foundNow {
		t.Fatal("reused syntax did not expose the current identifier")
	}
	if foundOld {
		t.Fatal("reused syntax retained the previous identifier")
	}
	if after.Symbols == before.Symbols {
		t.Fatal("symbols were reused after an identifier edit")
	}
}

func TestAnalyzePatchesChangedIdentifierReference(t *testing.T) {
	const revision = "project:1"
	beforeText := []byte("new first;\nnew other;\nstock Work() { return first; }\n")
	afterText := []byte("new first;\nnew other;\nstock Work() { return other; }\n")
	before := analysis.Analyze(beforeText, analysis.Options{Revision: revision})
	var reusedSymbols int
	after, err := analysis.AnalyzeContext(context.Background(), afterText, analysis.Options{
		Previous: before, Revision: revision, ReuseCompatibleExpansion: true,
		Trace: func(event analysis.TraceEvent) {
			if event.Stage == analysis.StageSymbolsOriginal {
				reusedSymbols = event.Reused
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reusedSymbols != 1 || after.Symbols == before.Symbols {
		t.Fatal("changed reference did not reuse the symbol table")
	}
	start := bytes.LastIndex(afterText, []byte("other"))
	span := source.Span{
		File: after.File, Start: source.Offset(start), End: source.Offset(start + len("other")),
	}
	item, ok := after.Symbols.ReferencedAt(span)
	if !ok || item.Name != "other" {
		t.Fatalf("resolved reference = %#v, found = %v", item, ok)
	}
	clean := analysis.Analyze(afterText, analysis.Options{Revision: revision})
	if !reflect.DeepEqual(after.Diagnostics, clean.Diagnostics) {
		t.Fatal("patched reference diagnostics differ from clean analysis")
	}
}

func TestAnalyzeRebuildsExpandedViewForNonLocalTokenEdit(t *testing.T) {
	tests := []struct {
		name   string
		before string
		after  string
	}{
		{
			name:   "signature",
			before: "stock Work() { return 1; }\n",
			after:  "stock Task() { return 1; }\n",
		},
		{
			name:   "macro invocation",
			before: "#define PICK(%0) (%0)\nstock Work() { return PICK(1); }\n",
			after:  "#define PICK(%0) (%0)\nstock Work() { return PICK(2); }\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := analysis.Analyze(
				[]byte(test.before),
				analysis.Options{RetainExpanded: true, Revision: "project:1"},
			)
			after := analysis.Analyze(
				[]byte(test.after),
				analysis.Options{
					RetainExpanded: true, Previous: before, Revision: "project:1",
					ReuseCompatibleExpansion: true,
				},
			)
			if after.ExpandedParse == before.ExpandedParse || after.ExpandedSymbols == before.ExpandedSymbols {
				t.Fatal("expanded view was reused")
			}
		})
	}
}

func TestAnalyzeKeepsExpandedOutputExactByDefault(t *testing.T) {
	includes := preprocess.MapResolver{"shared": bytes.Repeat([]byte("stock Included() {}\n"), 20)}
	before := analysis.Analyze(
		[]byte("#include <shared>\nstock Work() { return 1; }\n"),
		analysis.Options{Includes: includes, RetainExpanded: true, Revision: "project:1"},
	)
	after := analysis.Analyze(
		[]byte("#include <shared>\nstock Work() { return 2; }\n"),
		analysis.Options{
			Includes: includes, RetainExpanded: true, Previous: before, Revision: "project:1",
		},
	)
	if after.ExpandedParse == before.ExpandedParse || after.ExpandedSymbols == before.ExpandedSymbols {
		t.Fatal("exact analysis reused the previous expansion")
	}
	if after.Reuse.CompatibleExpansion {
		t.Fatal("exact analysis reported a compatible expansion")
	}
}

func TestAnalyzeReusesDependencyGraphForLocalInsertion(t *testing.T) {
	includes := preprocess.MapResolver{"shared": bytes.Repeat([]byte("stock Included() {}\n"), 20)}
	initial := []byte("#include <shared>\nstock Work() { return 1; }\nstock Keep() { return 2; }\n")
	final := []byte("#include <shared>\nstock Work() { return 1 + 2; }\nstock Keep() { return 2; }\n")
	opts := analysis.Options{
		Includes: includes, RetainExpanded: true, Revision: "project:1",
		ReuseCompatibleExpansion: true,
	}
	before := analysis.Analyze(initial, opts)
	opts.Previous = before
	after := analysis.Analyze(final, opts)
	if !after.Reuse.CompatibleExpansion {
		t.Fatal("dependency graph was not reused")
	}
	clean := analysis.Analyze(final, analysis.Options{
		Includes: includes, RetainExpanded: true, Revision: "project:1",
	})
	if !reflect.DeepEqual(after.Diagnostics, clean.Diagnostics) {
		t.Fatalf("incremental diagnostics differ:\ngot  %#v\nwant %#v", after.Diagnostics, clean.Diagnostics)
	}
}

func TestAnalyzeRebuildsExpandedViewWhenTriviaMovesTokens(t *testing.T) {
	before := analysis.Analyze(
		[]byte("stock  Work() { return 1; }\n"),
		analysis.Options{RetainExpanded: true},
	)
	after := analysis.Analyze(
		[]byte("stock Work()  { return 1; }\n"),
		analysis.Options{RetainExpanded: true, Previous: before},
	)
	if after.ExpandedParse == before.ExpandedParse || after.ExpandedSymbols == before.ExpandedSymbols {
		t.Fatal("expanded view reused with changed token origins")
	}
}

func TestAnalyzeDoesNotReuseMalformedDeclarations(t *testing.T) {
	before := analysis.Analyze([]byte("stock Keep() {}\n"), analysis.Options{})
	after := analysis.Analyze([]byte("stock Broken( {\n"), analysis.Options{Previous: before})
	if after.Declarations.Reliable() {
		t.Fatal("malformed declaration boundaries are reliable")
	}
	if after.Reuse.Declarations != 0 {
		t.Fatalf("reused declarations = %d, want 0", after.Reuse.Declarations)
	}
	if after.Reuse.ControlFlow != 0 || after.Reuse.Tags != 0 {
		t.Fatalf("malformed reuse = %+v", after.Reuse)
	}
}

func TestAnalyzeRootParseMatchesDirectParseCompact(t *testing.T) {
	text := []byte("#define TARGET Helper\nmain() { TARGET(); new x[10]; }\nHelper() { return 1; }\n")
	result := analysis.Analyze(text, analysis.Options{URI: source.FileURI("test.pwn")})
	want := parser.ParseCompact(text, parser.ParseOptions{})
	if !reflect.DeepEqual(result.Parse, want) {
		t.Fatalf("root parse diverged from a direct ParseCompact call:\ngot:  %+v\nwant: %+v", result.Parse, want)
	}
}

func TestAnalyzeAtCallbackWithConstArrays(t *testing.T) {
	result := analysis.Analyze([]byte("forward @receivestring(const message[], const source[]);\n"), analysis.Options{})
	if result.Parse.HasParseErrors() || len(result.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", result.Diagnostics)
	}
}

func TestAnalyzeKeepsOriginalSymbolOffsets(t *testing.T) {
	text := []byte("stock Helper() {}\nmain() { return Helper(); }")
	result := analysis.Analyze(text, analysis.Options{})
	for _, ref := range result.Symbols.References {
		if ref.Name == "Helper" && ref.IsCall {
			if got, want := int(ref.Span.Start), bytes.LastIndex(text, []byte("Helper")); got != want {
				t.Fatalf("reference offset = %d, want %d", got, want)
			}
			return
		}
	}
	t.Fatal("Helper call reference missing")
}

func TestAnalyzeKeepsUnknownExternalName(t *testing.T) {
	result := analysis.Analyze([]byte("main() { External(); }"), analysis.Options{})
	if len(result.Diagnostics) != 0 || len(result.Semantics.Unknown) != 1 {
		t.Fatalf("diagnostics=%d unknown=%d", len(result.Diagnostics), len(result.Semantics.Unknown))
	}
}

func TestAnalyzeReportsConfirmedMissingName(t *testing.T) {
	result := analysis.Analyze([]byte("main() { Missing(); }"), analysis.Options{Names: sema.MapResolver{}})
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "pawn-analysis:sema/undefined-symbol" {
		t.Fatalf("diagnostics: %+v", result.Diagnostics)
	}
	if result.Diagnostics[0].DocsURL != "https://github.com/pawnkit/pawn-analysis/blob/main/docs/diagnostics.md" {
		t.Fatalf("documentation URL = %q", result.Diagnostics[0].DocsURL)
	}
}

func TestAnalyzeMapsIncludeDiagnostics(t *testing.T) {
	result := analysis.Analyze([]byte("#include <bad>\nmain() {}"), analysis.Options{
		URI:      source.FileURI("main.pwn"),
		Includes: preprocess.MapResolver{"bad": []byte("#error broken\n")},
	})
	if len(result.Diagnostics) == 0 {
		t.Fatal("included diagnostic missing")
	}
	uri, ok := result.Registry.URI(result.Diagnostics[0].Primary.File)
	if !ok || uri.String() != "bad" {
		t.Fatalf("diagnostic URI = %q, ok=%v", uri, ok)
	}
}

func TestAnalyzeMapsExpandedIncludeSymbols(t *testing.T) {
	result := analysis.Analyze([]byte("#include <helper>\nmain() { Helper(); }"), analysis.Options{
		URI: source.FileURI("main.pwn"), RetainExpanded: true,
		Includes: preprocess.MapResolver{"helper": []byte("Helper() {}\n")},
	})
	for _, item := range result.ExpandedSymbols.Symbols {
		if item.Name != "Helper" {
			continue
		}
		uri, ok := result.Registry.URI(item.Span.File)
		if !ok || uri.String() != "helper" {
			t.Fatalf("Helper URI = %q, ok=%v", uri, ok)
		}
		return
	}
	t.Fatal("included Helper symbol missing")
}

func TestAnalyzeReportsIncludedRedeclaration(t *testing.T) {
	result := analysis.Analyze([]byte("#include <helper>\nmain() {}"), analysis.Options{
		URI: source.FileURI("main.pwn"), RetainExpanded: true,
		Includes: preprocess.MapResolver{"helper": []byte("stock Value;\nstock Value;\n")},
	})
	for _, item := range result.Diagnostics {
		if item.Code != "pawn-analysis:symbol/redeclared" {
			continue
		}
		uri, ok := result.Registry.URI(item.Primary.File)
		if !ok || uri.String() != "helper" {
			t.Fatalf("diagnostic URI = %q, ok=%v", uri, ok)
		}
		return
	}
	t.Fatal("included redeclaration missing")
}

func TestAnalyzeResolvesIncludedCallable(t *testing.T) {
	result := analysis.Analyze([]byte("#include <helper>\nmain() { Helper(); }"), analysis.Options{
		Includes: preprocess.MapResolver{"helper": []byte("stock Helper(value) { return value; }")},
		Names:    sema.MapResolver{},
	})
	var foundArity bool
	for _, item := range result.Diagnostics {
		if item.Code == "pawn-analysis:sema/undefined-symbol" {
			t.Fatalf("included callable unresolved: %+v", item)
		}
		foundArity = foundArity || item.Code == "pawn-analysis:sema/argument-count"
	}
	if !foundArity {
		t.Fatalf("included signature was not checked: %+v", result.Diagnostics)
	}
}

func TestMacroOverridesExternalCallableArity(t *testing.T) {
	text := []byte("#define SendClientMessage( va_SendClientMessage(\nmain() { SendClientMessage(1, 2, \"value %d\", 3); }\n")
	result := analysis.Analyze(text, analysis.Options{
		Names: callableResolver{"SendClientMessage": {MinArgs: 3, MaxArgs: 3}},
	})
	for _, item := range result.Diagnostics {
		if item.Code == "pawn-analysis:sema/argument-count" {
			t.Fatalf("macro call used external arity: %+v", item)
		}
	}
}

func TestAnalyzeMapsMacroSymbolToInvocation(t *testing.T) {
	text := []byte("#define NAME Helper\nNAME() {}\n")
	result := analysis.Analyze(text, analysis.Options{
		URI: source.FileURI("main.pwn"), RetainExpanded: true,
	})
	for _, item := range result.ExpandedSymbols.Symbols {
		if item.Name == "Helper" {
			if got, want := int(item.Span.Start), bytes.Index(text, []byte("NAME()")); got != want {
				t.Fatalf("macro symbol offset = %d, want %d", got, want)
			}
			return
		}
	}
	t.Fatal("expanded macro symbol missing")
}

func TestAnalyzeDoesNotRedeclareFunctionMacroInvocations(t *testing.T) {
	source := []byte("#define TEST(%0) forward test_%0(); public test_%0()\nTEST(one) {}\nTEST(two) {}\n")
	result := analysis.Analyze(source, analysis.Options{RetainExpanded: true})
	for _, item := range result.Diagnostics {
		if item.Code == "pawn-analysis:symbol/redeclared" {
			t.Fatalf("macro invocation reported as a redeclaration: %+v", item)
		}
	}
}

func TestAnalyzeLimitsExpandedOutput(t *testing.T) {
	result := analysis.Analyze([]byte("main() { new values[] = { 1, 2, 3, 4, 5, 6, 7, 8, 9 }; }\n"), analysis.Options{MaxOutputTokens: 8})
	for _, item := range result.Diagnostics {
		if item.Code == "pawn-analysis:preprocess/output-size-limit" {
			return
		}
	}
	t.Fatalf("output limit diagnostic missing: %+v", result.Diagnostics)
}

func BenchmarkAnalyze(b *testing.B) {
	text := []byte("#define SCALE(%0) ((%0) * 2)\nstock Helper(value) { return SCALE(value); }\nmain() { return Helper(21); }\n")
	b.ReportAllocs()
	for b.Loop() {
		analysis.Analyze(text, analysis.Options{})
	}
}

// syntheticGlobalGamemode builds many global functions calling earlier ones.
func syntheticGlobalGamemode(functions int) []byte {
	var sb strings.Builder
	sb.WriteString("#define MAX_PLAYERS 1000\n\n")
	for i := range functions {
		fmt.Fprintf(&sb, "stock Func%d(playerid, value) {\n", i)
		sb.WriteString("    new x = value + MAX_PLAYERS;\n")
		for c := 0; c < 5 && c < i; c++ {
			fmt.Fprintf(&sb, "    x = Func%d(playerid, x);\n", i-1-c)
		}
		sb.WriteString("    return x;\n}\n\n")
	}
	return []byte(sb.String())
}

// BenchmarkAnalyzeLargeGamemodeExpanded uses the RetainExpanded path pawnlsp uses.
func BenchmarkAnalyzeLargeGamemodeExpanded(b *testing.B) {
	text := syntheticGlobalGamemode(2000)
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		analysis.Analyze(text, analysis.Options{RetainExpanded: true})
	}
}

// BenchmarkAnalyzeGlobalScanScaling checks global name resolution scaling.
func BenchmarkAnalyzeGlobalScanScaling(b *testing.B) {
	for _, n := range []int{500, 1000, 2000, 4000} {
		text := syntheticGlobalGamemode(n)
		b.Run(fmt.Sprintf("functions=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for b.Loop() {
				analysis.Analyze(text, analysis.Options{RetainExpanded: true})
			}
		})
	}
}

func BenchmarkAnalyzeCancellation50K(b *testing.B) {
	text := syntheticGlobalGamemode(8000)
	var cancellationTotal time.Duration

	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		ctx, cancel := context.WithCancel(context.Background())
		var cancelledAt atomic.Int64
		_, err := analysis.AnalyzeContext(ctx, text, analysis.Options{
			RetainExpanded: true,
			Trace: func(event analysis.TraceEvent) {
				if event.Stage != analysis.StageParseOriginal {
					return
				}
				now := time.Now().UnixNano()
				if cancelledAt.CompareAndSwap(0, now) {
					cancel()
				}
			},
		})
		cancel()
		if !errors.Is(err, context.Canceled) {
			b.Fatalf("error = %v, want cancellation", err)
		}
		cancellationTotal += time.Since(time.Unix(0, cancelledAt.Load()))
	}
	b.ReportMetric(float64(cancellationTotal.Nanoseconds())/float64(b.N), "cancel-ns/op")
}
