package sema_test

import (
	"testing"

	"github.com/pawnkit/pawn-analysis/sema"
	parser "github.com/pawnkit/pawn-parser"
)

func TestEvalConstant(t *testing.T) {
	tests := []struct {
		text  string
		value int64
		known bool
	}{
		{"1 + 2 * 3", 7, true},
		{"(8 >> 1) == 4", 1, true},
		{"0 ? 10 : 20", 20, true},
		{"10 / 0", 0, false},
		{"unknown + 1", 0, false},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			file := parseCompact(t, "main() { return "+test.text+"; }")
			ret := findSyntaxKind(file.Syntax(), parser.KindReturnStatement)
			expression, ok := ret.Field("value")
			if !ok {
				t.Fatal("return value missing")
			}
			got := sema.EvalConstant(expression)
			if got.Known != test.known || got.Value != test.value {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestResolveConstants(t *testing.T) {
	text := `
const BASE = 2;
const ENABLED = BASE + 1;
enum Values { First, Second, Tenth = 10, Eleventh }
main() {}
`
	file := parseCompact(t, text)
	table := tableFor(t, text)
	values := sema.ResolveConstants(file.Syntax(), table)
	want := map[string]int64{"BASE": 2, "ENABLED": 3, "First": 0, "Second": 1, "Tenth": 10, "Eleventh": 11}
	for _, item := range table.Symbols {
		expected, ok := want[item.Name]
		if !ok {
			continue
		}
		if got := values[item.ID]; !got.Known || got.Value != expected {
			t.Errorf("%s = %+v, want %d", item.Name, got, expected)
		}
	}
}

func TestResolveConstantsRejectsCycle(t *testing.T) {
	text := "const A = B; const B = A; main() {}"
	file := parseCompact(t, text)
	values := sema.ResolveConstants(file.Syntax(), tableFor(t, text))
	if len(values) != 0 {
		t.Fatalf("values=%+v", values)
	}
}

func findSyntaxKind(root parser.SyntaxNode, kind parser.Kind) parser.SyntaxNode {
	if root.Kind() == kind {
		return root
	}
	it := root.Children()
	for it.Next() {
		if found := findSyntaxKind(it.Node(), kind); found.Valid() {
			return found
		}
	}
	return parser.SyntaxNode{}
}
