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
