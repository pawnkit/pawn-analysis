package sema_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pawnkit/pawn-analysis/sema"
)

func TestCheckConstantOrderContextStopsDuringTraversal(t *testing.T) {
	text := strings.Repeat("const VALUE = LATER;\n", 2_000) + "const LATER = 1;\n"
	file := parseCompact(t, text)
	ctx := &delayedCancelContext{after: 1}

	diagnostics, err := sema.CheckConstantOrderContext(ctx, file.Syntax(), tableFor(t, text))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if diagnostics != nil {
		t.Fatal("cancelled constant-order checking returned partial diagnostics")
	}
}

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
