package symbol_test

import (
	"reflect"
	"testing"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawn-parser/lexer"
	"github.com/pawnkit/pawnkit-core/source"
)

func TestRebaseParenthesizedMatchesCleanTable(t *testing.T) {
	t.Parallel()

	before := []byte("new value;\nstock Work() { return value; }\nstock Keep() { return value; }\n")
	after := []byte("new value;\nstock Work() { return (value); }\nstock Keep() { return value; }\n")
	file := source.FileID(1)
	previousParse := parser.ParseCompact(before, parser.ParseOptions{})
	previous := symbol.Build(previousParse.Syntax(), file)
	start := len("new value;\nstock Work() { return ")
	got, ok := symbol.RebaseParenthesized(
		previous,
		before,
		after,
		lexer.Tokenize(before),
		lexer.Tokenize(after),
		parser.ByteRange{Start: start, End: start + len("value")},
		parser.ByteRange{Start: start, End: start + len("(value)")},
	)
	if !ok {
		t.Fatal("parenthesized table was not rebased")
	}
	cleanParse := parser.ParseCompact(after, parser.ParseOptions{})
	clean := symbol.Build(cleanParse.Syntax(), file)
	if !reflect.DeepEqual(got.Symbols, clean.Symbols) ||
		!reflect.DeepEqual(got.References, clean.References) {
		t.Fatal("rebased table differs from clean table")
	}
}

func TestRebaseParenthesizedRejectsCallChange(t *testing.T) {
	t.Parallel()

	before := []byte("stock Work() { return Other; }\n")
	after := []byte("stock Work() { return Other(); }\n")
	file := source.FileID(1)
	parsed := parser.ParseCompact(before, parser.ParseOptions{})
	previous := symbol.Build(parsed.Syntax(), file)
	start := len("stock Work() { return Other")
	if _, ok := symbol.RebaseParenthesized(
		previous,
		before,
		after,
		lexer.Tokenize(before),
		lexer.Tokenize(after),
		parser.ByteRange{Start: start, End: start},
		parser.ByteRange{Start: start, End: start + 2},
	); ok {
		t.Fatal("call-changing edit rebased the symbol table")
	}
}
