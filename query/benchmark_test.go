package query

import (
	"context"
	"fmt"
	"testing"

	analysis "github.com/pawnkit/pawn-analysis"
	"github.com/pawnkit/pawnkit-core/source"
)

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
