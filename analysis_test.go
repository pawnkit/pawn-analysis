package analysis_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	analysis "github.com/pawnkit/pawn-analysis"
	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawn-analysis/sema"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/source"
)

func TestAnalyzeContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := analysis.AnalyzeContext(ctx, []byte("main() {}"), analysis.Options{})
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("AnalyzeContext() = (%v, %v), want cancellation", result, err)
	}
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
		analysis.StageSymbolsOriginal,
		analysis.StageParseExpanded,
		analysis.StageSymbolsExpanded,
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
