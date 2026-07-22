package sema_test

import (
	"testing"

	"github.com/pawnkit/pawn-analysis/sema"
)

func TestControlFlowDiagnostics(t *testing.T) {
	tests := []struct {
		name string
		text string
		code string
		want int
	}{
		{"unreachable", "main() { return 1; new value; }", "pawn-analysis:sema/unreachable", 1},
		{"tagged fallthrough", "Float:GetValue(flag) { if (flag) return 1.0; }", "pawn-analysis:sema/missing-return", 1},
		{"void fallthrough", "void:Reset() { value = 0; }", "", 0},
		{"all paths return", "Float:GetValue(flag) { if (flag) return 1.0; else return 2.0; }", "", 0},
		{"concise return", "signed:Convert(value) return signed:value;", "", 0},
		{"concise operator return", "stock unsigned:operator=(value) return unsigned:value;", "", 0},
		{"concise expression list return", "native String:Result(Task:task); stock String:Await(Task:task) return Wait(task), Result(task);", "", 0},
		{"after break", "main() { while (value) { break; value++; } return 1; }", "pawn-analysis:sema/unreachable", 1},
		{"after continue", "main() { while (value) { continue; value++; } return 1; }", "pawn-analysis:sema/unreachable", 1},
		{"returning switch", "Float:GetValue(value) { switch (value) { case 1: return 1.0; default: return 0.0; } }", "", 0},
		{"switch without default", "Float:GetValue(value) { switch (value) { case 1: return 1.0; } }", "pawn-analysis:sema/missing-return", 1},
		{"after switch break", "main() { switch (value) { case 1: { break; value++; } } return 1; }", "pawn-analysis:sema/unreachable", 1},
		{"forward goto", "main() { goto done; value++; done: return 1; }", "pawn-analysis:sema/unreachable", 1},
		{"undefined label", "main() { goto missing; }", "pawn-analysis:sema/undefined-label", 1},
		{"constant false branch", "main() { if (0) value++; else return 1; }", "pawn-analysis:sema/unreachable", 1},
		{"constant false loop", "main() { while (0) value++; return 1; }", "pawn-analysis:sema/unreachable", 1},
		{"constant true loop", "Float:GetValue() { while (1) { value++; } }", "", 0},
		{"returning do while", "Float:GetValue() { do { return 1.0; } while (0); }", "", 0},
		{"const false branch", "const ENABLED = 0; main() { if (ENABLED) value++; return 1; }", "pawn-analysis:sema/unreachable", 1},
		{"enum true loop", "enum { Disabled, Enabled }; Float:GetValue() { while (Enabled) {} }", "", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := parseCompact(t, test.text)
			table := tableFor(t, test.text)
			flows, diagnostics := sema.CheckControlFlow(file.Syntax(), table)
			if len(flows) != 1 || len(diagnostics) != test.want {
				t.Fatalf("flows=%d diagnostics=%+v", len(flows), diagnostics)
			}
			if test.want != 0 && diagnostics[0].Code != test.code {
				t.Fatalf("code=%q", diagnostics[0].Code)
			}
		})
	}
}
