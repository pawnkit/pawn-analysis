package sema_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pawnkit/pawn-analysis/sema"
)

func TestCheckTagsContextStopsDuringTraversal(t *testing.T) {
	text := "main() { new Float:value; new bool:other;" +
		strings.Repeat("value = other;", 2_000) + "}"
	file := parseCompact(t, text)
	table := tableFor(t, text)
	ctx := &delayedCancelContext{after: 1}

	diagnostics, cache, reused, err := sema.CheckTagsCachedContext(
		ctx, file.Syntax(), table, nil, nil, "",
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if diagnostics != nil || cache != nil || reused != 0 {
		t.Fatal("cancelled tag checking returned partial results")
	}
}

func TestTagMismatchChecks(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"initializer", "main() { new Float:value = bool:true; }", 1},
		{"assignment", "main() { new Float:value; new bool:other; value = other; }", 1},
		{"binary", "main() { new Float:value; new bool:other; return value + other; }", 1},
		{"ternary", "main() { new Float:a; new bool:b; return true ? a : b; }", 1},
		{"return", "Float:GetValue() { return bool:true; }", 1},
		{"call argument", "UseFloat(Float:value) {} main() { UseFloat(bool:true); }", 1},
		{"matching call", "UseFloat(Float:value) {} main() { UseFloat(1.0); }", 0},
		{"matching", "Float:GetValue() { new Float:value = 1.0; return value; }", 0},
		{"unknown remains unknown", "Float:GetValue() { return External(); }", 0},
		{"tag union float", "Accept({Float, bool}:value) {} main() { Accept(1.0); }", 0},
		{"tag union bool", "Accept({Float, bool}:value) {} main() { Accept(bool:true); }", 0},
		{"tag union mismatch", "Accept({Float, bool}:value) {} main() { Accept(String:1); }", 1},
		{"weak tag union", "Accept({Float, _}:value) {} main() { Accept(String:1); }", 0},
		{"weak expected tag", "Accept(_:value) {} main() { Accept(Float:1); }", 0},
		{"binary operator overload", "Float:operator+(bool:left, Float:right) { return right; } Float:Get() { return bool:true + 1.0; }", 0},
		{"operator overload mismatch", "Float:operator+(bool:left, Float:right) { return right; } main() { new bool:left; new String:right; return left + right; }", 1},
		{"comparison result", "Float:GetValue() { return 1 == 1; }", 1},
		{"logical negation result", "Float:GetValue() { return !1; }", 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := tableFor(t, test.text)
			file := parseCompact(t, test.text)
			got := sema.CheckTags(file.Syntax(), table, nil)
			if len(got) != test.want {
				t.Fatalf("diagnostics = %+v", got)
			}
		})
	}
}

func TestExternalCallTags(t *testing.T) {
	resolver := signatureResolver{"Native": {
		ReturnTag: "Float", ParamTags: []string{"Float"}, MinArgs: 1, MaxArgs: 1,
	}}
	text := "main() { new bool:value; value = Native(bool:true); }"
	table := tableFor(t, text)
	file := parseCompact(t, text)
	diagnostics := sema.CheckTags(file.Syntax(), table, resolver)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
}
