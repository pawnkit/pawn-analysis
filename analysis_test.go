package analysis_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	analysis "github.com/pawnkit/pawn-analysis"
	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawn-analysis/sema"
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

func BenchmarkAnalyze(b *testing.B) {
	text := []byte("#define SCALE(%0) ((%0) * 2)\nstock Helper(value) { return SCALE(value); }\nmain() { return Helper(21); }\n")
	b.ReportAllocs()
	for b.Loop() {
		analysis.Analyze(text, analysis.Options{})
	}
}
