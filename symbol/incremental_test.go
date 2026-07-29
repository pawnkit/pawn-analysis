package symbol_test

import (
	"testing"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/source"
)

func TestPatchReferenceUpdatesOneUse(t *testing.T) {
	const text = "new first;\nnew other;\nmain() { first = 1; }\n"
	file := source.FileID(1)
	table := symbol.Build(parser.ParseWithProfile([]byte(text), parser.ProfileAnalysis).Syntax(), file)
	start := len("new first;\nnew other;\nmain() { ")
	span := source.Span{File: file, Start: source.Offset(start), End: source.Offset(start + len("first"))}

	next, ok := symbol.PatchReference(table, span, "other")
	if !ok {
		t.Fatal("reference was not patched")
	}
	item, ok := next.ReferencedAt(span)
	if !ok || item.Name != "other" {
		t.Fatalf("resolved reference = %#v, found = %v", item, ok)
	}
	if original, ok := table.ReferencedAt(span); !ok || original.Name != "first" {
		t.Fatal("previous table was modified")
	}
}

func TestPatchReferenceRejectsDeclaration(t *testing.T) {
	const text = "new first;\n"
	file := source.FileID(1)
	table := symbol.Build(parser.ParseWithProfile([]byte(text), parser.ProfileAnalysis).Syntax(), file)
	span := source.Span{File: file, Start: 4, End: 9}
	if _, ok := symbol.PatchReference(table, span, "other"); ok {
		t.Fatal("declaration was patched as a reference")
	}
}

func TestPatchReferenceRejectsUnresolvedNames(t *testing.T) {
	const text = "main() { missing = 1; }\n"
	file := source.FileID(1)
	table := symbol.Build(parser.ParseWithProfile([]byte(text), parser.ProfileAnalysis).Syntax(), file)
	start := len("main() { ")
	span := source.Span{File: file, Start: source.Offset(start), End: source.Offset(start + len("missing"))}
	if _, ok := symbol.PatchReference(table, span, "unknown"); ok {
		t.Fatal("unresolved reference was patched")
	}
}
