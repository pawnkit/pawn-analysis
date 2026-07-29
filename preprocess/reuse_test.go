package preprocess_test

import (
	"context"
	"testing"

	"github.com/pawnkit/pawn-analysis/preprocess"
)

func TestReuseTriviaContext(t *testing.T) {
	before := preprocess.Run([]byte("stock Work() { return 1; } // old\n"), preprocess.Options{})
	after, reused, err := preprocess.ReuseTriviaContext(
		context.Background(),
		[]byte("stock Work() { return 1; } // new\n"),
		"input.pwn",
		nil,
		before,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reused {
		t.Fatal("preprocessing was not reused")
	}
	if string(after.Source) != "stock Work() { return 1; } // new\n" {
		t.Fatalf("source = %q", after.Source)
	}
}

func TestReuseTriviaContextRejectsMovedTokens(t *testing.T) {
	before := preprocess.Run([]byte("stock  Work() { return 1; }\n"), preprocess.Options{})
	_, reused, err := preprocess.ReuseTriviaContext(
		context.Background(),
		[]byte("stock Work()  { return 1; }\n"),
		"input.pwn",
		nil,
		before,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reused {
		t.Fatal("preprocessing reused after token positions changed")
	}
}

func TestReuseCompatibleContext(t *testing.T) {
	before := preprocess.Run([]byte("stock Work() { return 1; }\n"), preprocess.Options{})
	after, edit, reused, err := preprocess.ReuseCompatibleContext(
		context.Background(),
		[]byte("stock Work() { return 2; }\n"),
		"input.pwn",
		nil,
		before,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reused || edit.Before.Start == edit.Before.End || edit.After.Start == edit.After.End {
		t.Fatalf("reused = %v, edit = %#v", reused, edit)
	}
	if string(after.Source) != "stock Work() { return 2; }\n" {
		t.Fatalf("source = %q", after.Source)
	}
}

func TestReuseCompatibleContextShiftsFollowingDirectives(t *testing.T) {
	initial := []byte("stock Work() { return 1; }\n#include <shared>\n")
	final := []byte("stock Work() { return 1 + 2; }\n#include <shared>\n")
	before := preprocess.Run(initial, preprocess.Options{})
	after, edit, reused, err := preprocess.ReuseCompatibleContext(
		context.Background(), final, "input.pwn", nil, before,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reused {
		t.Fatal("preprocessing was not reused")
	}
	if len(after.Includes) != 1 {
		t.Fatalf("includes = %#v", after.Includes)
	}
	delta := edit.After.End - edit.Before.End
	if got, want := after.Includes[0].DirectiveSpan.Start, before.Includes[0].DirectiveSpan.Start+delta; got != want {
		t.Fatalf("include start = %d, want %d", got, want)
	}
}

func TestReuseCompatibleContextShiftsFollowingMacroInvocations(t *testing.T) {
	initial := []byte("#define VALUE 1\nstock Work() { return 1; }\nnew result = VALUE;\n")
	final := []byte("#define VALUE 1\nstock Work() { return 1 + 2; }\nnew result = VALUE;\n")
	before := preprocess.Run(initial, preprocess.Options{})
	after, edit, reused, err := preprocess.ReuseCompatibleContext(
		context.Background(), final, "input.pwn", nil, before,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reused {
		t.Fatal("preprocessing was not reused")
	}
	if len(after.MacroInvocations) != 1 {
		t.Fatalf("invocations = %#v", after.MacroInvocations)
	}
	delta := edit.After.End - edit.Before.End
	if got, want := after.MacroInvocations[0].Range.Start, before.MacroInvocations[0].Range.Start+delta; got != want {
		t.Fatalf("invocation start = %d, want %d", got, want)
	}
}

func TestReuseCompatibleContextRejectsDirectiveEdit(t *testing.T) {
	before := preprocess.Run([]byte("#define VALUE 1\nstock Work() { return VALUE; }\n"), preprocess.Options{})
	_, _, reused, err := preprocess.ReuseCompatibleContext(
		context.Background(),
		[]byte("#define VALUE 2\nstock Work() { return VALUE; }\n"),
		"input.pwn",
		nil,
		before,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reused {
		t.Fatal("preprocessing reused after a directive edit")
	}
}
