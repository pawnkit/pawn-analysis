package sema_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pawnkit/pawn-analysis/sema"
)

func TestCheckStatesContextStopsDuringTraversal(t *testing.T) {
	text := strings.Repeat("Handler() <ready> {}\n", 2_000)
	file := parseCompact(t, text)
	ctx := &delayedCancelContext{after: 1}

	diagnostics, err := sema.CheckStatesContext(ctx, file.Syntax(), 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if diagnostics != nil {
		t.Fatal("cancelled state checking returned partial diagnostics")
	}
}

func TestStateChecks(t *testing.T) {
	tests := []struct {
		name string
		text string
		code string
	}{
		{"default state", "main() { state ready; } Handler() <ready> {}", ""},
		{"named state", "main() { state traffic:ready; } Handler() <traffic:ready> {}", ""},
		{"unknown state", "main() { state missing; } Handler() <ready> {}", "pawn-analysis:sema/unknown-state"},
		{"unknown automaton", "main() { state missing:ready; } Handler() <traffic:ready> {}", "pawn-analysis:sema/unknown-automaton"},
		{"state conflict", "Handler() <ready> {} Handler() <ready> {}", "pawn-analysis:sema/state-conflict"},
		{"automaton conflict", "Handler() <first:ready> {} Handler() <second:waiting> {}", "pawn-analysis:sema/automaton-conflict"},
		{"missing used implementation", "main() { Handler(); } Handler() <ready> {} Defines() <waiting> {}", "pawn-analysis:sema/missing-state-implementation"},
		{"fallback covers state", "main() { Handler(); } Handler() <ready> {} Handler() <> {} Defines() <waiting> {}", ""},
		{"public needs coverage", "public Handler() <ready> {} Defines() <waiting> {}", "pawn-analysis:sema/missing-state-implementation"},
		{"native state", "native Handler() <ready>; Defines() <ready> {}", "pawn-analysis:sema/invalid-state-function"},
		{"fallback only", "main() { Handler(); } Handler() <> {}", "pawn-analysis:sema/no-defined-states"},
		{"local state variable", "main() { new value <ready>; } Defines() <ready> {}", "pawn-analysis:sema/invalid-state-variable"},
		{"state variable shadow", "new value; new value <ready>; Defines() <ready> {}", "pawn-analysis:sema/state-variable-shadow"},
		{"iterator capacity", "new Iterator:values[MAX_GROUPS]<MAX_PLAYERS>;", ""},
		{"forward state ignored", "forward Handler() <ready>; Handler() <ready> {}", "pawn-analysis:sema/forward-state-ignored"},
		{"initialized state variable", "Defines() <ready> {} Defines() <waiting> {} new value <ready> = 1; new value <waiting> = 2;", "pawn-analysis:sema/initialized-state-variable"},
		{"empty variable states", "new value <>;", "pawn-analysis:sema/no-defined-states"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := parseCompact(t, test.text)
			diagnostics := sema.CheckStates(file.Syntax(), 1)
			if test.code == "" && len(diagnostics) != 0 {
				t.Fatalf("diagnostics = %+v", diagnostics)
			}
			if test.code != "" && (len(diagnostics) != 1 || diagnostics[0].Code != test.code) {
				t.Fatalf("diagnostics = %+v", diagnostics)
			}
		})
	}
}
