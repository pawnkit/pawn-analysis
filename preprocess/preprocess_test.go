package preprocess_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pawnkit/pawn-analysis/preprocess"
)

type delayedCancelContext struct {
	checks atomic.Int32
	after  int32
}

func TestForwardingMacroExposesReplacementCallable(t *testing.T) {
	result := preprocess.Run([]byte("#define PlayerDialog_Show(%0,%1, \\\n Dialog_Open(%0,#%1,\n"), preprocess.Options{})
	macro, ok := result.Macros["PlayerDialog_Show"]
	if !ok {
		t.Fatal("macro missing")
	}
	if got, ok := macro.ReplacementCallable(); !ok || got != "Dialog_Open" {
		t.Fatalf("replacement callable = %q, %v; macro = %#v", got, ok, macro)
	}
}

func (c *delayedCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *delayedCancelContext) Done() <-chan struct{}       { return nil }
func (c *delayedCancelContext) Value(any) any               { return nil }
func (c *delayedCancelContext) Err() error {
	if c.checks.Add(1) > c.after {
		return context.Canceled
	}
	return nil
}

func expandedText(t *testing.T, r *preprocess.Result) string {
	t.Helper()
	var sb strings.Builder
	for _, tok := range r.ExpandedTokens {
		if tok.Kind.String() == "EOF" {
			continue
		}
		sb.WriteString(tok.Text(r.ExpandedSource))
		sb.WriteByte(' ')
	}
	return sb.String()
}

func TestRunContextStopsDuringPreprocessing(t *testing.T) {
	src := []byte("#define DUP(%0) %0 %0\n" + strings.Repeat("DUP(value)\n", 2_000))
	ctx := &delayedCancelContext{after: 1}

	result, err := preprocess.RunContext(ctx, src, preprocess.Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if result != nil {
		t.Fatal("cancelled preprocessing returned a result")
	}
}

func TestObjectMacroExpansion(t *testing.T) {
	src := "#define MAX_ZONES 32\nnew zones = MAX_ZONES;\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	if len(r.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", r.Diagnostics)
	}
	got := expandedText(t, r)
	if !strings.Contains(got, "zones = 32") {
		t.Fatalf("expected object macro expansion, got %q", got)
	}
}

func TestFunctionMacroExpansion(t *testing.T) {
	src := "#define SQR(%0) ((%0) * (%0))\nnew x = SQR(zones + 1);\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	got := expandedText(t, r)
	want := "( ( zones + 1 ) * ( zones + 1 ) )"
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q in %q", want, got)
	}
}

func TestFunctionMacroArgumentRepetition(t *testing.T) {
	src := "#define TWICE(%0) %0 %0\nTWICE(zones++;)\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	got := expandedText(t, r)
	if strings.Count(got, "zones") != 2 {
		t.Fatalf("expected argument to be repeated twice, got %q", got)
	}
}

func TestFunctionMacroFinalParameterConsumesCommas(t *testing.T) {
	src := "#define COPY(%0,%1) (%0 = %1)\nCOPY(value, 1, 2)\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	if len(r.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", r.Diagnostics)
	}
	if got := string(r.ExpandedSource); !strings.Contains(got, "value = 1 , 2") {
		t.Fatalf("expanded source = %q", got)
	}
}

func TestFunctionMacroAcceptsEmptyArgument(t *testing.T) {
	src := "#define EMPTY(%0) value\nEMPTY()\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	if len(r.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", r.Diagnostics)
	}
	if got := string(r.ExpandedSource); !strings.Contains(got, "value") {
		t.Fatalf("expanded source = %q", got)
	}
}

func TestEmitDirectiveIsAccepted(t *testing.T) {
	r := preprocess.Run([]byte("#emit CONST.pri 1\n"), preprocess.Options{})
	if len(r.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", r.Diagnostics)
	}
}

func TestPrefixMacroDoesNotReportArgumentMismatch(t *testing.T) {
	src := "#define main( ALS_main_:PP_main(\nmain() {}\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	if len(r.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", r.Diagnostics)
	}
}

func TestMultiArgFunctionMacro(t *testing.T) {
	src := "#define CLAMP(%0,%1,%2) ((%0) < (%1) ? (%1) : ((%0) > (%2) ? (%2) : (%0)))\n" +
		"new c = CLAMP(zones, 0, 16);\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	if len(r.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", r.Diagnostics)
	}
	got := expandedText(t, r)
	if !strings.Contains(got, "zones") || !strings.Contains(got, "16") {
		t.Fatalf("expected substitution, got %q", got)
	}
}

func TestMacroParameterLabelsUseDeclarationOrder(t *testing.T) {
	src := "#define PICK(%1) (%1)\n" +
		"#define PAIR(%0,%2) ((%0) + (%2))\n" +
		"new value = PICK(3) + PAIR(4, 5);\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	if len(r.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", r.Diagnostics)
	}
	got := expandedText(t, r)
	if !strings.Contains(got, "( 3 )") || !strings.Contains(got, "( ( 4 ) + ( 5 ) )") {
		t.Fatalf("expected declaration-order substitution, got %q", got)
	}
}

func TestNamedParamMacro(t *testing.T) {
	src := "#define ADD(a,b) ((a) + (b))\nnew x = ADD(1, 2);\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	got := expandedText(t, r)
	if !strings.Contains(got, "( 1 ) + ( 2 )") {
		t.Fatalf("expected named-param substitution, got %q", got)
	}
}

func TestMacroSelfReferenceDoesNotRecurse(t *testing.T) {
	src := "#define FOO FOO + 1\nnew x = FOO;\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	got := expandedText(t, r)
	if !strings.Contains(got, "FOO + 1") {
		t.Fatalf("expected one level of self-reference expansion, got %q", got)
	}
}

func TestUndef(t *testing.T) {
	src := "#define FOO 1\n#undef FOO\nnew x = FOO;\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	got := expandedText(t, r)
	if !strings.Contains(got, "x = FOO") {
		t.Fatalf("expected FOO to stay unexpanded after #undef, got %q", got)
	}
}

func TestConditionalCompilationDefined(t *testing.T) {
	src := "#define FEATURE\n#if defined FEATURE\nnew a = 1;\n#else\nnew a = 2;\n#endif\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	got := expandedText(t, r)
	if !strings.Contains(got, "a = 1") || strings.Contains(got, "a = 2") {
		t.Fatalf("expected the defined() branch to be active, got %q", got)
	}
	if len(r.Branches) != 2 {
		t.Fatalf("expected 2 branches, got %d: %+v", len(r.Branches), r.Branches)
	}
	if !r.Branches[0].Active || r.Branches[1].Active {
		t.Fatalf("branch activity mismatch: %+v", r.Branches)
	}
}

func TestConditionalCompilationElseif(t *testing.T) {
	src := "#define HUD_VERSION 2\n" +
		"#if HUD_VERSION >= 2\nnew v = 2;\n#elseif HUD_VERSION == 1\nnew v = 1;\n#else\nnew v = 0;\n#endif\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	got := expandedText(t, r)
	if !strings.Contains(got, "v = 2") {
		t.Fatalf("expected first branch active, got %q", got)
	}
}

func TestConditionalNested(t *testing.T) {
	src := "#if 1\n#if 0\nnew a = 1;\n#else\nnew a = 2;\n#endif\n#endif\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	got := expandedText(t, r)
	if !strings.Contains(got, "a = 2") || strings.Contains(got, "a = 1") {
		t.Fatalf("expected nested else branch, got %q", got)
	}
}

func TestInactiveBranchDefinesDoNotLeak(t *testing.T) {
	src := "#if 0\n#define SHOULD_NOT_EXIST 1\n#endif\nnew x = SHOULD_NOT_EXIST;\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	got := expandedText(t, r)
	if !strings.Contains(got, "x = SHOULD_NOT_EXIST") {
		t.Fatalf("macro defined in inactive branch must not take effect, got %q", got)
	}
}

func TestIncludeGuardPattern(t *testing.T) {
	resolver := preprocess.MapResolver{
		"helper.inc": []byte("#if defined _INC_HELPER\n#endinput\n#endif\n#define _INC_HELPER\nstock Helper() { return 1; }\n"),
	}
	src := "#include \"helper.inc\"\n#include \"helper.inc\"\nmain() { Helper(); }\n"
	r := preprocess.Run([]byte(src), preprocess.Options{Resolver: resolver})
	if len(r.Includes) != 2 {
		t.Fatalf("expected 2 include directives recorded, got %d", len(r.Includes))
	}
	got := expandedText(t, r)
	if strings.Count(got, "stock") != 1 {
		t.Fatalf("expected include guard to prevent double inclusion, got %q", got)
	}
}

func TestIncludeGuardBreaksRecursiveInclude(t *testing.T) {
	resolver := preprocess.MapResolver{
		"a.inc": []byte("#if defined _INC_A\n#endinput\n#endif\n#define _INC_A\n#include \"b.inc\"\n"),
		"b.inc": []byte("#include \"a.inc\"\n"),
	}
	result := preprocess.Run([]byte("#include \"a.inc\"\n"), preprocess.Options{Resolver: resolver})
	for _, item := range result.Diagnostics {
		if item.Code == preprocess.CodeIncludeCycle {
			t.Fatalf("guarded include reported a cycle: %+v", item)
		}
	}
}

func TestUnguardedRecursiveIncludeReportsCycle(t *testing.T) {
	resolver := preprocess.MapResolver{
		"a.inc": []byte("#include \"b.inc\"\n"),
		"b.inc": []byte("#include \"a.inc\"\n"),
	}
	result := preprocess.Run([]byte("#include \"a.inc\"\n"), preprocess.Options{Resolver: resolver})
	for _, item := range result.Diagnostics {
		if item.Code == preprocess.CodeIncludeCycle {
			return
		}
	}
	t.Fatalf("cycle diagnostic missing: %+v", result.Diagnostics)
}

func TestTokenCacheProducesIdenticalResultsAcrossRuns(t *testing.T) {
	resolver := preprocess.MapResolver{
		"helper.inc": []byte("#define HELPER_VALUE 7\nstock Helper() { return HELPER_VALUE; }\n"),
	}
	src := "#include \"helper.inc\"\nmain() { Helper(); }\n"
	cache := preprocess.NewTokenCache()

	first := preprocess.Run([]byte(src), preprocess.Options{Resolver: resolver, TokenCache: cache})
	second := preprocess.Run([]byte(src), preprocess.Options{Resolver: resolver, TokenCache: cache})

	firstText := expandedText(t, first)
	secondText := expandedText(t, second)
	if firstText != secondText {
		t.Fatalf("expanded output changed across cached runs:\n%q\n%q", firstText, secondText)
	}
	if len(first.Diagnostics) != 0 || len(second.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v / %v", first.Diagnostics, second.Diagnostics)
	}
}

func TestTryIncludeMissingIsSilent(t *testing.T) {
	src := "#tryinclude <does_not_exist>\nnew x = 1;\n"
	r := preprocess.Run([]byte(src), preprocess.Options{Resolver: preprocess.MapResolver{}})
	for _, d := range r.Diagnostics {
		if d.Code == preprocess.CodeIncludeNotFound {
			t.Fatalf("tryinclude of a missing file must not be an error: %+v", d)
		}
	}
}

func TestIncludeMissingIsError(t *testing.T) {
	src := "#include <does_not_exist>\nnew x = 1;\n"
	r := preprocess.Run([]byte(src), preprocess.Options{Resolver: preprocess.MapResolver{}})
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == preprocess.CodeIncludeNotFound {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected include-not-found diagnostic, got %+v", r.Diagnostics)
	}
}

func TestNoResolverLeavesIncludeUnresolved(t *testing.T) {
	src := "#include <a_samp>\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	if len(r.Includes) != 1 || r.Includes[0].Resolved {
		t.Fatalf("expected an unresolved include record, got %+v", r.Includes)
	}
	for _, d := range r.Diagnostics {
		if d.Code == preprocess.CodeIncludeNotFound {
			t.Fatalf("no resolver configured should not itself be an error: %+v", d)
		}
	}
}

func TestErrorAndWarningDirectives(t *testing.T) {
	src := "#warning something is off\n#if 0\n#error should not fire\n#endif\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	var warn, errs int
	for _, d := range r.Diagnostics {
		switch d.Code {
		case preprocess.CodeUserWarning:
			warn++
		case preprocess.CodeUserError:
			errs++
		}
	}
	if warn != 1 || errs != 0 {
		t.Fatalf("expected 1 warning and 0 errors, got warn=%d errs=%d diags=%+v", warn, errs, r.Diagnostics)
	}
}

func TestAssertDirective(t *testing.T) {
	src := "#assert 1 == 2\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == preprocess.CodeAssertFailed {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an assert-failed diagnostic, got %+v", r.Diagnostics)
	}
}

func TestEndinputStopsProcessing(t *testing.T) {
	src := "new a = 1;\n#endinput\nnew b = 2;\n"
	r := preprocess.Run([]byte(src), preprocess.Options{})
	got := expandedText(t, r)
	if !strings.Contains(got, "a = 1") || strings.Contains(got, "b = 2") {
		t.Fatalf("expected content after #endinput to be dropped, got %q", got)
	}
}

func TestPredefinedMacros(t *testing.T) {
	src := "new a = OPEN_MP;\n"
	r := preprocess.Run([]byte(src), preprocess.Options{Predefined: map[string]string{"OPEN_MP": "1"}})
	got := expandedText(t, r)
	if !strings.Contains(got, "a = 1") {
		t.Fatalf("expected predefined macro expansion, got %q", got)
	}
}

func TestDeterministicOutput(t *testing.T) {
	src := "#define SQR(%0) ((%0)*(%0))\nnew x = SQR(1+2);\n#if defined SQR\nnew y = 1;\n#endif\n"
	r1 := preprocess.Run([]byte(src), preprocess.Options{})
	r2 := preprocess.Run([]byte(src), preprocess.Options{})
	if expandedText(t, r1) != expandedText(t, r2) {
		t.Fatalf("expected deterministic expansion output")
	}
	if len(r1.Branches) != len(r2.Branches) || len(r1.Diagnostics) != len(r2.Diagnostics) {
		t.Fatalf("expected deterministic branch/diagnostic counts")
	}
}
