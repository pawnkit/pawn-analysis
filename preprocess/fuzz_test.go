package preprocess_test

import (
	"testing"
	"time"

	"github.com/pawnkit/pawn-analysis/preprocess"
)

// FuzzRun exercises the directive/macro-expansion pipeline against
// arbitrary byte input, asserting only the invariants that must hold no
// matter how malformed the input is: Run never panics, always terminates,
// and never exceeds its configured bounds.
func FuzzRun(f *testing.F) {
	seeds := []string{
		"",
		"#define FOO 1\nnew x = FOO;\n",
		"#define SQR(%0) ((%0)*(%0))\nSQR(1+2)\n",
		"#if defined FOO\n#else\n#endif\n",
		"#if 1\n#elseif 0\n#else\n#endif\n",
		"#include <a>\n#tryinclude \"b\"\n",
		"#define A A\nA\n",
		"#define A(%0) A(%0) A(%0)\nA(x)\n",
		"#undef FOO\n#error msg\n#warning msg\n#assert 1\n",
		"#endinput\nnever\n",
		"\\\n#define FOO\\\n 1\n",
		"#if 1\n#if 1\n#if 1\n#endif\n#endif\n#endif\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 8192 {
			t.Skip()
		}
		done := make(chan struct{})
		var r *preprocess.Result
		go func() {
			defer close(done)
			r = preprocess.Run(src, preprocess.Options{
				MaxExpansionDepth:   16,
				MaxConditionalDepth: 64,
				MaxIncludeDepth:     8,
				MaxOutputTokens:     20000,
			})
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("Run did not terminate within budget for input %q", src)
		}
		if r == nil {
			t.Fatalf("Run returned nil")
		}
		if len(r.ExpandedTokens) > 200000 {
			t.Fatalf("expanded token count %d exceeds any reasonable bound", len(r.ExpandedTokens))
		}
	})
}
