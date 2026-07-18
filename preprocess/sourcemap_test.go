package preprocess_test

import (
	"testing"

	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawn-parser/token"
)

func TestSourceMapThroughMacroExpansion(t *testing.T) {
	src := "#define SQR(%0) ((%0) * (%0))\nnew x = SQR(zones);\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})

	var found *token.Token
	for i := range r.ExpandedTokens {
		tok := r.ExpandedTokens[i]
		if tok.Text(r.ExpandedSource) == "zones" && tok.Origin != nil {
			found = &r.ExpandedTokens[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a substituted 'zones' token carrying an Origin")
	}
	origin := found.Origin
	if origin.Macro != "SQR" {
		t.Fatalf("expected Origin.Macro == SQR, got %q", origin.Macro)
	}
	invocationText := string(r.Source[origin.Span.Start.Offset:origin.Span.End.Offset])
	if invocationText != "SQR(zones)" {
		t.Fatalf("expected Origin.Span to cover the invocation 'SQR(zones)', got %q", invocationText)
	}
	if origin.Parent == nil {
		t.Fatal("expected argument spelling origin")
	}
	argumentText := string(r.Files[origin.Parent.Span.File].Content[origin.Parent.Span.Start.Offset:origin.Parent.Span.End.Offset])
	if argumentText != "zones" {
		t.Fatalf("expected argument origin 'zones', got %q", argumentText)
	}
}

func TestSourceMapNestedExpansion(t *testing.T) {
	src := "#define INNER(%0) ((%0)+1)\n#define OUTER(%0) INNER(%0)\nnew x = OUTER(5);\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})

	found := false
	for _, tok := range r.ExpandedTokens {
		if tok.Text(r.ExpandedSource) == "5" && tok.Origin != nil {
			if tok.Origin.Macro == "INNER" && tok.Origin.Parent != nil && tok.Origin.Parent.Macro == "OUTER" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected the literal '5' argument to carry a two-level Origin chain (INNER <- OUTER)")
	}
}

func TestSourceMapLiteralBodyToken(t *testing.T) {
	src := "#define GREETING hello_world\nnew x = GREETING;\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})

	var found *token.Token
	for i := range r.ExpandedTokens {
		tok := r.ExpandedTokens[i]
		if tok.Text(r.ExpandedSource) == "hello_world" {
			found = &r.ExpandedTokens[i]
			break
		}
	}
	if found == nil || found.Origin == nil {
		t.Fatalf("expected the expanded literal body token to carry an Origin")
	}
	if found.Origin.Macro != "GREETING" {
		t.Fatalf("expected Origin.Macro == GREETING, got %q", found.Origin.Macro)
	}
	invoked := string(r.Source[found.Origin.Span.Start.Offset:found.Origin.Span.End.Offset])
	if invoked != "GREETING" {
		t.Fatalf("expected Origin.Span to resolve to the invocation site 'GREETING', got %q", invoked)
	}
	if found.Origin.Parent == nil {
		t.Fatal("expected macro definition origin")
	}
	definition := found.Origin.Parent.Span
	defined := string(r.Files[definition.File].Content[definition.Start.Offset:definition.End.Offset])
	if defined != "hello_world" {
		t.Fatalf("expected definition origin 'hello_world', got %q", defined)
	}
}

func TestSourceMapMacroDefinedInInclude(t *testing.T) {
	src := "#include \"defs.inc\"\nnew x = VALUE;\n"
	r := preprocess.Run([]byte(src), preprocess.Options{Resolver: preprocess.MapResolver{
		"defs.inc": []byte("#define VALUE included_value\n"),
	}})

	for _, tok := range r.ExpandedTokens {
		if tok.Text(r.ExpandedSource) != "included_value" {
			continue
		}
		if tok.Origin == nil || tok.Origin.Parent == nil {
			t.Fatal("expected invocation and definition origins")
		}
		invocation := tok.Origin.Span
		if invocation.File != 0 || string(r.Files[0].Content[invocation.Start.Offset:invocation.End.Offset]) != "VALUE" {
			t.Fatal("expected invocation in the root file")
		}
		definition := tok.Origin.Parent.Span
		if r.Files[definition.File].URI != "defs.inc" {
			t.Fatalf("expected definition in defs.inc, got %q", r.Files[definition.File].URI)
		}
		if string(r.Files[definition.File].Content[definition.Start.Offset:definition.End.Offset]) != "included_value" {
			t.Fatal("expected definition token in included file")
		}
		return
	}
	t.Fatal("expected expanded include macro token")
}
