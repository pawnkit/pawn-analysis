package query

import (
	"testing"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/source"
)

func TestWorkspaceResolverCachesFingerprint(t *testing.T) {
	resolver := newWorkspaceResolver(nil)
	first := resolver.fingerprint()
	if first != resolver.fingerprint() {
		t.Fatal("repeated fingerprint changed")
	}

	file := parser.ParseCompact([]byte("stock Helper() {}\n"), parser.ParseOptions{})
	registry := source.NewRegistry()
	resolver.add(symbol.Build(file.Syntax(), registry.Intern(source.FileURI("helper.inc"))))
	second := resolver.fingerprint()
	if second == first {
		t.Fatal("fingerprint did not change after adding symbols")
	}
	if second != resolver.fingerprint() {
		t.Fatal("cached fingerprint changed")
	}
}
