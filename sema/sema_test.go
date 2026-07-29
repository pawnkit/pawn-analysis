package sema_test

import (
	"context"
	"errors"
	"reflect"
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

func TestCheckNamesCachedContextReusesUnchangedFunction(t *testing.T) {
	before := tableFor(t, "stock First() { MissingA(); }\nstock Second() { MissingB(); }\n")
	_, cache, reused, err := sema.CheckNamesCachedContext(
		context.Background(), before, sema.MapResolver{}, nil, "project:1",
	)
	if err != nil || reused != 0 {
		t.Fatalf("first check: reused=%d err=%v", reused, err)
	}

	after := tableFor(t, "stock First() { { MissingA(); } }\nstock Second() { MissingB(); }\n")
	got, _, reused, err := sema.CheckNamesCachedContext(
		context.Background(), after, sema.MapResolver{}, cache, "project:1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if reused != 1 {
		t.Fatalf("reused functions = %d, want 1", reused)
	}
	want := sema.CheckNames(after, sema.MapResolver{})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cached result differs:\ngot  %#v\nwant %#v", got, want)
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
