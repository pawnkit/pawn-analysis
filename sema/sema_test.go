package sema_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pawnkit/pawn-analysis/sema"
	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/source"
)

type delayedCancelContext struct {
	checks atomic.Int32
	after  int32
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

func tableFor(t *testing.T, text string) *symbol.Table {
	t.Helper()
	file := parser.ParseCompact([]byte(text), parser.ParseOptions{})
	if file == nil {
		t.Fatal("parser returned nil")
	}
	return symbol.Build(file.Syntax(), source.FileID(1))
}

func TestCheckNamesContextStopsDuringResolution(t *testing.T) {
	table := tableFor(t, "main() {"+strings.Repeat("Missing();", 2_000)+"}")
	ctx := &delayedCancelContext{after: 1}

	result, err := sema.CheckNamesContext(ctx, table, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if len(result.Diagnostics) != 0 || len(result.Unknown) != 0 {
		t.Fatal("cancelled name resolution returned partial results")
	}
}

func parseCompact(t *testing.T, text string) *parser.CompactFile {
	t.Helper()
	file := parser.ParseCompact([]byte(text), parser.ParseOptions{})
	if file == nil {
		t.Fatal("parser returned nil")
	}
	return file
}

func TestUnknownWithoutProjectContext(t *testing.T) {
	result := sema.CheckNames(tableFor(t, "main() { ExternalCall(); }"), nil)
	if len(result.Diagnostics) != 0 || len(result.Unknown) != 1 {
		t.Fatalf("got diagnostics=%d unknown=%d", len(result.Diagnostics), len(result.Unknown))
	}
}

func TestExternalNameResolves(t *testing.T) {
	resolver := sema.MapResolver{"ExternalCall": {}}
	result := sema.CheckNames(tableFor(t, "main() { ExternalCall(); }"), resolver)
	if len(result.Diagnostics) != 0 || len(result.Unknown) != 0 {
		t.Fatalf("got diagnostics=%d unknown=%d", len(result.Diagnostics), len(result.Unknown))
	}
}

func TestConfirmedMissingNameIsDiagnostic(t *testing.T) {
	result := sema.CheckNames(tableFor(t, "main() { MissingCall(); }"), sema.MapResolver{})
	if len(result.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics", len(result.Diagnostics))
	}
	if result.Diagnostics[0].Code != "pawn-analysis:sema/undefined-symbol" {
		t.Fatalf("code = %q", result.Diagnostics[0].Code)
	}
}

func TestCallingVariableIsDiagnostic(t *testing.T) {
	result := sema.CheckNames(tableFor(t, "main() { new value; value(); }"), nil)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics", len(result.Diagnostics))
	}
	if result.Diagnostics[0].Code != "pawn-analysis:sema/not-callable" {
		t.Fatalf("code = %q", result.Diagnostics[0].Code)
	}
}

func TestCallingFunctionIsValid(t *testing.T) {
	result := sema.CheckNames(tableFor(t, "Helper() {} main() { Helper(); }"), nil)
	if len(result.Diagnostics) != 0 || len(result.Unknown) != 0 {
		t.Fatalf("got diagnostics=%d unknown=%d", len(result.Diagnostics), len(result.Unknown))
	}
}

func TestCallArgumentCount(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"too few", "Helper(a, b) {} main() { Helper(1); }", 1},
		{"too many", "Helper(a) {} main() { Helper(1, 2); }", 1},
		{"default", "Helper(a, b = 2) {} main() { Helper(1); }", 0},
		{"variadic", "Helper(a, ...) {} main() { Helper(1, 2, 3); }", 0},
		{"ysi variadic", "Helper(a, va_args<>) {} main() { Helper(1, 2, 3); }", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := sema.CheckNames(tableFor(t, test.text), nil)
			if len(result.Diagnostics) != test.want {
				t.Fatalf("got diagnostics: %+v", result.Diagnostics)
			}
		})
	}
}

type signatureResolver map[string]sema.Callable

func (r signatureResolver) ResolveName(name string) sema.NameState {
	if _, ok := r[name]; ok {
		return sema.NameFound
	}
	return sema.NameUnknown
}

func (r signatureResolver) ResolveCallable(name string) (sema.Callable, bool) {
	value, ok := r[name]
	return value, ok
}

func TestExternalCallArgumentCount(t *testing.T) {
	resolver := signatureResolver{"Native": {MinArgs: 1, MaxArgs: 2}}
	result := sema.CheckNames(tableFor(t, "main() { Native(); }"), resolver)
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "pawn-analysis:sema/argument-count" {
		t.Fatalf("diagnostics: %+v", result.Diagnostics)
	}
}

func TestCheckNamesCachedReusesUnchangedFunction(t *testing.T) {
	const calls = 20
	beforeText := fmt.Sprintf(
		"stock Stable() { %s }\nstock Changed() { %s }",
		strings.Repeat("MissingStable();", calls), strings.Repeat("MissingChanged();", calls),
	)
	afterText := fmt.Sprintf(
		"stock Stable() { %s }\nstock Changed() { %s }",
		strings.Repeat("MissingStable();", calls), strings.Repeat("MissingOther();", calls),
	)

	before := parseCompact(t, beforeText)
	beforeTable := symbol.Build(before.Syntax(), source.FileID(1))
	beforeIndex := parser.BuildDeclarationIndex(before)
	_, cache, _, err := sema.CheckNamesCachedIndexedContext(
		context.Background(), before.Syntax(), beforeTable, sema.MapResolver{}, nil, "revision", beforeIndex,
	)
	if err != nil {
		t.Fatalf("initial check: %v", err)
	}

	after := parseCompact(t, afterText)
	afterTable := symbol.Build(after.Syntax(), source.FileID(1))
	afterIndex := parser.BuildDeclarationIndex(after)
	got, _, reused, err := sema.CheckNamesCachedIndexedContext(
		context.Background(), after.Syntax(), afterTable, sema.MapResolver{}, cache, "revision", afterIndex,
	)
	if err != nil {
		t.Fatalf("incremental check: %v", err)
	}
	if reused != 1 {
		t.Fatalf("reused functions = %d, want 1", reused)
	}

	want := sema.CheckNames(afterTable, sema.MapResolver{})
	if len(got.Diagnostics) != len(want.Diagnostics) {
		t.Fatalf("diagnostics = %d, want %d", len(got.Diagnostics), len(want.Diagnostics))
	}
	for i := range got.Diagnostics {
		if got.Diagnostics[i].Code != want.Diagnostics[i].Code ||
			got.Diagnostics[i].Message != want.Diagnostics[i].Message ||
			got.Diagnostics[i].Primary != want.Diagnostics[i].Primary {
			t.Fatalf("diagnostic %d = %+v, want %+v", i, got.Diagnostics[i], want.Diagnostics[i])
		}
	}
}
