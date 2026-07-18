package symbol_test

import (
	"testing"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/source"
)

// FuzzBuild checks malformed trees because editors call Build mid-edit.
func FuzzBuild(f *testing.F) {
	seeds := []string{
		"",
		"stock Foo(a, b) { new x = a + b; return x; }",
		"enum E { A, B, C };",
		"public OnFoo(playerid) { new Float:x = 1.0; return x; }",
		"stock Foo() { for (new i = 0; i < 10; i++) { new j = i; } }",
		"native Bar(a, b, c);",
		"stock",
		"stock Foo(",
		"{{{{{{{{{{",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 4096 {
			t.Skip()
		}
		file := parser.ParseCompact(src, parser.ParseOptions{})
		if file == nil {
			return
		}
		reg := source.NewRegistry()
		id := reg.Intern(source.FileURI("fuzz.pwn"))
		_ = symbol.Build(file.Syntax(), id)
	})
}
