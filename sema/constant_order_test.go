package sema_test

import (
	"testing"

	"github.com/pawnkit/pawn-analysis/sema"
)

func TestConstantDeclarationOrder(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"earlier constant", "const A = 1; const B = A;", 0},
		{"self reference", "const A = A;", 1},
		{"forward reference", "const A = B; const B = 2;", 1},
		{"mutual reference", "const A = B; const B = A;", 1},
		{"enum forward reference", "enum Values { First = Second, Second = 2 }", 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := parseCompact(t, test.text)
			diagnostics := sema.CheckConstantOrder(file.Syntax(), tableFor(t, test.text))
			if len(diagnostics) != test.want {
				t.Fatalf("diagnostics = %+v", diagnostics)
			}
		})
	}
}
