package query

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	analysis "github.com/pawnkit/pawn-analysis"
	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawnkit-core/source"
)

// syntheticGlobalGamemode builds many global functions calling earlier ones.
func syntheticGlobalGamemode(functions int) []byte {
	var sb strings.Builder
	sb.WriteString("#define MAX_PLAYERS 1000\n\n")
	for i := range functions {
		fmt.Fprintf(&sb, "stock Func%d(playerid, value) {\n", i)
		sb.WriteString("    new x = value + MAX_PLAYERS;\n")
		sb.WriteString("    new y = x;\n")
		for c := 0; c < 5 && c < i; c++ {
			fmt.Fprintf(&sb, "    x = Func%d(playerid, x);\n", i-1-c)
		}
		sb.WriteString("    if (x > 10) x--;\n")
		sb.WriteString("    return x;\n}\n\n")
	}
	return []byte(sb.String())
}

// BenchmarkAnalyzeWorkspaceLargeSingleFile matches the reported large single-file scenario.
func BenchmarkAnalyzeWorkspaceLargeSingleFile(b *testing.B) {
	uri := source.FileURI("gamemode.pwn")
	text := syntheticGlobalGamemode(2000)
	snapshot := New(Document{URI: uri, Text: text, Version: 1})
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		if _, err := snapshot.AnalyzeWorkspace(ctx, analysis.Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSnapshotCachedAnalysis(b *testing.B) {
	uri := source.FileURI("main.pwn")
	snapshot := New(Document{
		URI: uri, Text: []byte("stock Helper(value) { return value + 1; } main() { return Helper(1); }"), Version: 1,
	})
	ctx := context.Background()
	if _, err := snapshot.Analyze(ctx, uri, analysis.Options{}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := snapshot.Analyze(ctx, uri, analysis.Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSnapshotCachedLargeAnalysis(b *testing.B) {
	uri := source.FileURI("gamemode.pwn")
	text := syntheticGlobalGamemode(2000)
	snapshot := New(Document{URI: uri, Text: text, Version: 1})
	ctx := context.Background()
	if _, err := snapshot.Analyze(ctx, uri, analysis.Options{}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := snapshot.Analyze(ctx, uri, analysis.Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSnapshotUpdateLargeDocument(b *testing.B) {
	text := syntheticGlobalGamemode(2000)
	uri := source.FileURI("gamemode.pwn")
	for _, benchmark := range []struct {
		name  string
		owned bool
	}{
		{name: "copy"},
		{name: "owned", owned: true},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			snapshot := New()
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for version := int64(1); b.Loop(); version++ {
				document := Document{URI: uri, Text: text, Version: version}
				if benchmark.owned {
					snapshot, _ = snapshot.UpdateOwned(document)
				} else {
					snapshot, _ = snapshot.Update(document)
				}
			}
		})
	}
}

func BenchmarkIncrementalFunctionAnalysis(b *testing.B) {
	const functions = 2000
	uri := source.FileURI("gamemode.pwn")
	text := syntheticGlobalGamemode(functions)
	edit := strings.LastIndex(string(text), "x > 10") + len("x > 1")
	snapshot := New(Document{URI: uri, Text: text, Version: 1})
	opts := analysis.Options{Revision: "benchmark", ReuseCompatibleExpansion: true}
	if _, err := snapshot.Analyze(context.Background(), uri, opts); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	stageTotals := make(map[analysis.Stage]int64)
	for version := int64(2); b.Loop(); version++ {
		b.StopTimer()
		nextText := append([]byte(nil), text...)
		nextText[edit] = byte('0' + version%2)
		next, ok := snapshot.Update(Document{URI: uri, Text: nextText, Version: version})
		if !ok {
			b.Fatal("update rejected")
		}
		b.StartTimer()

		runOpts := opts
		runOpts.Trace = func(event analysis.TraceEvent) {
			stageTotals[event.Stage] += event.Duration.Nanoseconds()
		}
		result, err := next.Analyze(context.Background(), uri, runOpts)
		if err != nil {
			b.Fatal(err)
		}
		if result.Reuse.ControlFlow < functions-10 {
			b.Fatalf("reused CFGs = %d, want at least %d", result.Reuse.ControlFlow, functions-10)
		}
		if result.Reuse.Tags < functions-10 {
			b.Fatalf("reused tag checks = %d, want at least %d", result.Reuse.Tags, functions-10)
		}
		snapshot = next
		text = nextText
	}
	for stage, total := range stageTotals {
		b.ReportMetric(float64(total)/float64(b.N), string(stage)+"-ns/op")
	}
}

func BenchmarkIncrementalIdentifierReferenceAnalysis(b *testing.B) {
	const functions = 2000
	uri := source.FileURI("gamemode.pwn")
	text := syntheticGlobalGamemode(functions)
	edit := strings.LastIndex(string(text), "return x") + len("return ")
	snapshot := New(Document{URI: uri, Text: text, Version: 1})
	opts := analysis.Options{Revision: "benchmark", ReuseCompatibleExpansion: true}
	if _, err := snapshot.Analyze(context.Background(), uri, opts); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for version := int64(2); b.Loop(); version++ {
		b.StopTimer()
		nextText := append([]byte(nil), text...)
		replacement := byte('y')
		if version%2 != 0 {
			replacement = 'x'
		}
		nextText[edit] = replacement
		next, ok := snapshot.Update(Document{URI: uri, Text: nextText, Version: version})
		if !ok {
			b.Fatal("update rejected")
		}
		reusedSymbols := 0
		runOpts := opts
		runOpts.Trace = func(event analysis.TraceEvent) {
			if event.Stage == analysis.StageSymbolsOriginal {
				reusedSymbols = event.Reused
			}
		}
		b.StartTimer()

		if _, err := next.Analyze(context.Background(), uri, runOpts); err != nil {
			b.Fatal(err)
		}
		if reusedSymbols != 1 {
			b.Fatal("symbol table was rebuilt")
		}
		snapshot = next
		text = nextText
	}
}

func BenchmarkIncrementalParenthesizedAnalysis(b *testing.B) {
	const functions = 2000
	uri := source.FileURI("gamemode.pwn")
	base := syntheticGlobalGamemode(functions)
	edit := strings.LastIndex(string(base), "return x") + len("return ")
	text := append([]byte(nil), base...)
	snapshot := New(Document{URI: uri, Text: text, Version: 1})
	opts := analysis.Options{Revision: "benchmark", ReuseCompatibleExpansion: true}
	if _, err := snapshot.Analyze(context.Background(), uri, opts); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for version := int64(2); b.Loop(); version++ {
		b.StopTimer()
		var nextText []byte
		if version%2 == 0 {
			nextText = make([]byte, 0, len(base)+2)
			nextText = append(nextText, base[:edit]...)
			nextText = append(nextText, '(')
			nextText = append(nextText, base[edit])
			nextText = append(nextText, ')')
			nextText = append(nextText, base[edit+1:]...)
		} else {
			nextText = append([]byte(nil), base...)
		}
		next, ok := snapshot.Update(Document{URI: uri, Text: nextText, Version: version})
		if !ok {
			b.Fatal("update rejected")
		}
		b.StartTimer()

		result, err := next.Analyze(context.Background(), uri, opts)
		if err != nil {
			b.Fatal(err)
		}
		if !result.Reuse.ReparsedDeclaration {
			b.Fatal("declaration was fully reparsed")
		}
		snapshot = next
	}
}

func BenchmarkIncrementalTriviaAnalysis(b *testing.B) {
	const functions = 2000
	uri := source.FileURI("gamemode.pwn")
	text := append(syntheticGlobalGamemode(functions), []byte("// revision a\n")...)
	edit := len(text) - len("a\n")
	snapshot := New(Document{URI: uri, Text: text, Version: 1})
	previous, err := snapshot.Analyze(
		context.Background(), uri, analysis.Options{RetainExpanded: true, Revision: "project:1"},
	)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for version := int64(2); b.Loop(); version++ {
		b.StopTimer()
		nextText := append([]byte(nil), text...)
		nextText[edit] = byte('a' + version%2)
		next, ok := snapshot.Update(Document{URI: uri, Text: nextText, Version: version})
		if !ok {
			b.Fatal("update rejected")
		}
		b.StartTimer()

		result, err := next.Analyze(
			context.Background(), uri, analysis.Options{RetainExpanded: true, Revision: "project:1"},
		)
		if err != nil {
			b.Fatal(err)
		}
		if result.ExpandedParse != previous.ExpandedParse {
			b.Fatal("expanded parse was rebuilt")
		}
		snapshot, text, previous = next, nextText, result
	}
}

func BenchmarkIncrementalShiftedTriviaAnalysis(b *testing.B) {
	uri := source.FileURI("gamemode.pwn")
	base := append([]byte("#include <shared>\n"), syntheticGlobalGamemode(1000)...)
	shifted := bytes.Replace(base, []byte("    new x"), []byte("     new x"), 1)
	includes := preprocess.MapResolver{"shared": syntheticGlobalGamemode(2500)}
	opts := analysis.Options{
		Includes: includes, RetainExpanded: true, Revision: "project:1",
		ReuseCompatibleExpansion: true,
	}
	snapshot := New(Document{URI: uri, Text: base, Version: 1})
	if _, err := snapshot.Analyze(context.Background(), uri, opts); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for version := int64(2); b.Loop(); version++ {
		b.StopTimer()
		text := shifted
		if version%2 != 0 {
			text = base
		}
		next, ok := snapshot.Update(Document{URI: uri, Text: text, Version: version})
		if !ok {
			b.Fatal("update rejected")
		}
		var reusedParse int
		runOpts := opts
		runOpts.Trace = func(event analysis.TraceEvent) {
			if event.Stage == analysis.StageParseOriginal {
				reusedParse = event.Reused
			}
		}
		b.StartTimer()

		if _, err := next.Analyze(context.Background(), uri, runOpts); err != nil {
			b.Fatal(err)
		}
		if reusedParse != 1 {
			b.Fatal("original syntax was rebuilt")
		}
		snapshot = next
	}
}

func BenchmarkCleanFunctionAnalysis(b *testing.B) {
	uri := source.FileURI("gamemode.pwn")
	text := syntheticGlobalGamemode(2000)
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for version := int64(1); b.Loop(); version++ {
		snapshot := New(Document{URI: uri, Text: text, Version: version})
		if _, err := snapshot.Analyze(context.Background(), uri, analysis.Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAnalyzeWorkspace100Files(b *testing.B) {
	documents := make([]Document, 100)
	for i := range documents {
		name := fmt.Sprintf("Function%d", i)
		text := fmt.Sprintf("stock %s(value) { return value + %d; }", name, i)
		if i > 0 {
			text += fmt.Sprintf(" stock Use%d() { return Function%d(%d); }", i, i-1, i)
		}
		documents[i] = Document{
			URI: source.FileURI(fmt.Sprintf("file%d.pwn", i)), Text: []byte(text), Version: 1,
		}
	}
	snapshot := New(documents...)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		if _, err := snapshot.AnalyzeWorkspace(ctx, analysis.Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
