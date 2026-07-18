package preprocess_test

import (
	"testing"

	"github.com/pawnkit/pawn-analysis/preprocess"
)

// TestMalformedInputsDoNotPanic feeds truncated directives, unmatched
// conditionals, and other broken constructs through Run, asserting only
// that it returns without panicking; pawn-parser's CST is already
// error-tolerant and preprocess must be too, since editors call this on
// in-progress source constantly.
func TestMalformedInputsDoNotPanic(t *testing.T) {
	cases := []string{
		"",
		"#",
		"#d",
		"#define",
		"#define FOO",
		"#define FOO(",
		"#define FOO(%0",
		"#define FOO(%0)",
		"#if",
		"#if 1",
		"#if (",
		"#if defined",
		"#if defined(",
		"#elseif 1\n",
		"#else\n",
		"#endif\n",
		"#if 1\n#if 1\n#if 1\n",
		"#include",
		"#include <",
		"#include <unterminated\n",
		"#include \"unterminated\n",
		"#tryinclude\n",
		"#undef\n",
		"#assert\n",
		"#error\n",
		"#warning\n",
		"#endinput extra tokens ignored\n",
		"#unknown_directive stuff\n",
		"FOO(",
		"FOO(1,2",
		"#define FOO(%0) %0\nFOO(",
		"#define A A A A A A A A A A A A A A A A\nA\n",
		"\\\n#define FOO 1\n",
		"#define FOO(%0) %0(%1)\nFOO(x",
		strRepeat("#if 1\n", 2000) + strRepeat("#endif\n", 2000),
		strRepeat("#define M M\n", 1) + "M\n",
	}
	for _, src := range cases {
		src := src
		t.Run("", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on input %q: %v", src, r)
				}
			}()
			r := preprocess.Run([]byte(src), preprocess.Options{})
			if r == nil {
				t.Fatalf("Run returned nil for input %q", src)
			}
		})
	}
}

func strRepeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for range n {
		out = append(out, s...)
	}
	return string(out)
}

func TestMacroExpansionBudgetsBound(t *testing.T) {
	src := "#define A0 xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n"
	for i := 1; i <= 30; i++ {
		src += "#define A" + itoa(i) + " A" + itoa(i-1) + " A" + itoa(i-1) + "\n"
	}
	src += "A30\n"
	r := preprocess.Run([]byte(src), preprocess.Options{MaxOutputTokens: 10000, MaxExpansionDepth: 40})
	if !r.Truncated {
		t.Fatalf("expected exponential macro blowup to be flagged as truncated")
	}
	if len(r.ExpandedTokens) > 20000 {
		t.Fatalf("expected output to be bounded, got %d tokens", len(r.ExpandedTokens))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestUnmatchedDirectivesReportDiagnosticsNotPanic(t *testing.T) {
	cases := map[string]preprocess.Code{
		"#endif\n":    preprocess.CodeUnmatchedEndif,
		"#else\n":     preprocess.CodeUnmatchedElse,
		"#elseif 1\n": preprocess.CodeUnmatchedElseif,
	}
	for src, want := range cases {
		r := preprocess.Run([]byte(src), preprocess.Options{})
		found := false
		for _, d := range r.Diagnostics {
			if d.Code == want {
				found = true
			}
		}
		if !found {
			t.Errorf("input %q: expected diagnostic %s, got %+v", src, want, r.Diagnostics)
		}
	}
}

func TestUnterminatedConditionalReported(t *testing.T) {
	r := preprocess.Run([]byte("#if 1\nnew x = 1;\n"), preprocess.Options{})
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == preprocess.CodeUnterminatedConditional {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unterminated-conditional diagnostic, got %+v", r.Diagnostics)
	}
}
