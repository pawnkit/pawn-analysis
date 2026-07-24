package query

import (
	"context"
	"fmt"
	"strings"
	"testing"

	analysis "github.com/pawnkit/pawn-analysis"
	"github.com/pawnkit/pawnkit-core/source"
)

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
