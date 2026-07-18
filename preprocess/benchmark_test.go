package preprocess_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pawnkit/pawn-analysis/preprocess"
)

func syntheticGamemode(functions int) []byte {
	var sb strings.Builder
	sb.WriteString("#define MAX_PLAYERS 1000\n#define SQR(%0) ((%0)*(%0))\n\n")
	for i := range functions {
		fmt.Fprintf(&sb, "stock Func%d(playerid, value) {\n", i)
		fmt.Fprintf(&sb, "    new x = SQR(value) + MAX_PLAYERS;\n")
		fmt.Fprintf(&sb, "    if (playerid < MAX_PLAYERS) { x += 1; }\n")
		fmt.Fprintf(&sb, "    return x;\n}\n\n")
	}
	return []byte(sb.String())
}

func macroHeavySource(macros int) []byte {
	var sb strings.Builder
	for i := range macros {
		fmt.Fprintf(&sb, "#define M%d(%%0) ((%%0) + %d)\n", i, i)
	}
	sb.WriteString("stock Use() {\n    new x = 0;\n")
	for i := range macros {
		fmt.Fprintf(&sb, "    x = M%d(x);\n", i)
	}
	sb.WriteString("    return x;\n}\n")
	return []byte(sb.String())
}

func BenchmarkRunTinyFile(b *testing.B) {
	src := []byte("#define FOO 1\nstock Get() { return FOO; }\n")
	b.ReportAllocs()
	for b.Loop() {
		preprocess.Run(src, preprocess.Options{})
	}
}

func BenchmarkRunNormalGamemode(b *testing.B) {
	src := syntheticGamemode(50)
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		preprocess.Run(src, preprocess.Options{})
	}
}

func BenchmarkRunLargeGamemode(b *testing.B) {
	src := syntheticGamemode(2000)
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		preprocess.Run(src, preprocess.Options{})
	}
}

func BenchmarkRunMacroHeavy(b *testing.B) {
	src := macroHeavySource(500)
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		preprocess.Run(src, preprocess.Options{})
	}
}

func BenchmarkRunMalformed(b *testing.B) {
	src := []byte(strings.Repeat("#if 1\n#define X\nnew a = ;\n", 200) + strings.Repeat("#endif\n", 150))
	b.ReportAllocs()
	for b.Loop() {
		preprocess.Run(src, preprocess.Options{})
	}
}
