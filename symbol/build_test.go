package symbol_test

import (
	"testing"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/source"
)

func buildTable(t *testing.T, src string) (*symbol.Table, source.FileID) {
	t.Helper()
	file := parser.ParseCompact([]byte(src), parser.ParseOptions{})
	if file == nil {
		t.Fatalf("ParseCompact returned nil")
	}
	reg := source.NewRegistry()
	fileID := reg.Intern(source.FileURI("test.pwn"))
	table := symbol.Build(file.Syntax(), fileID)
	return table, fileID
}

func findSymbol(table *symbol.Table, name string) (symbol.Symbol, bool) {
	for _, s := range table.Symbols {
		if s.Name == name {
			return s, true
		}
	}
	return symbol.Symbol{}, false
}

func TestDeclareThenReference(t *testing.T) {
	src := "stock DoWork(value) { new total = 0; return total + value; }\n"
	table, _ := buildTable(t, src)

	fn, ok := findSymbol(table, "DoWork")
	if !ok || fn.Kind != symbol.KindStock {
		t.Fatalf("expected DoWork to be a stock function symbol, got %+v ok=%v", fn, ok)
	}
	total, ok := findSymbol(table, "total")
	if !ok || total.Kind != symbol.KindVariable {
		t.Fatalf("expected total to be a variable symbol, got %+v ok=%v", total, ok)
	}

	resolvedTotal, resolvedValue := false, false
	for _, ref := range table.References {
		if ref.Name == "total" && ref.Resolved == total.ID {
			resolvedTotal = true
		}
		if ref.Name == "value" && ref.Resolved != 0 {
			resolvedValue = true
		}
	}
	if !resolvedTotal {
		t.Errorf("expected a reference to 'total' resolving to its declaration")
	}
	if !resolvedValue {
		t.Errorf("expected the parameter 'value' to resolve inside the function body")
	}
	if len(table.Diagnostics) != 0 {
		t.Errorf("expected no diagnostics, got %+v", table.Diagnostics)
	}
}

func TestSpanIndexes(t *testing.T) {
	table, _ := buildTable(t, "new value;\nmain() { return value; }\n")
	declared, ok := findSymbol(table, "value")
	if !ok {
		t.Fatal("missing value declaration")
	}
	if got, found := table.DeclarationAt(declared.Span); !found || got.ID != declared.ID {
		t.Fatalf("declaration lookup = %+v, %v", got, found)
	}
	for _, reference := range table.References {
		if reference.Name != "value" {
			continue
		}
		if got, found := table.ReferencedAt(reference.Span); !found || got.ID != declared.ID {
			t.Fatalf("reference lookup = %+v, %v", got, found)
		}
		return
	}
	t.Fatal("missing value reference")
}

func TestStableIDsIgnoreFunctionBodiesAndOffsets(t *testing.T) {
	first, _ := buildTable(t, "stock Helper(value) { return value; }\nmain() {}\n")
	second, _ := buildTable(t, "\nstock Helper(value) { return value + 1; }\nmain() {}\n")

	firstHelper, firstOK := findSymbol(first, "Helper")
	secondHelper, secondOK := findSymbol(second, "Helper")
	if !firstOK || !secondOK {
		t.Fatal("missing Helper symbol")
	}
	firstID, firstStable := first.StableSymbolID(firstHelper.ID)
	secondID, secondStable := second.StableSymbolID(secondHelper.ID)
	if !firstStable || !secondStable || firstID != secondID {
		t.Fatal("function body or offset changed the stable ID")
	}
	if first.ExportFingerprint() != second.ExportFingerprint() {
		t.Fatal("function body or offset changed the export fingerprint")
	}
}

func TestExportFingerprintTracksSignatures(t *testing.T) {
	first, _ := buildTable(t, "stock Helper(value) { return value; }\n")
	second, _ := buildTable(t, "stock Float:Helper(Float:value) { return value; }\n")
	if first.ExportFingerprint() == second.ExportFingerprint() {
		t.Fatal("signature change did not update the export fingerprint")
	}
}

func TestRedeclarationInSameScope(t *testing.T) {
	src := "stock DoWork(value) { new total = 0; new total = 1; return total + value; }\n"
	table, _ := buildTable(t, src)

	if len(table.Diagnostics) != 1 {
		t.Fatalf("expected exactly 1 redeclaration diagnostic, got %d: %+v", len(table.Diagnostics), table.Diagnostics)
	}
	if table.Diagnostics[0].Code != "pawn-analysis:symbol/redeclared" {
		t.Errorf("unexpected diagnostic code: %s", table.Diagnostics[0].Code)
	}
}

func TestScopeShadowing(t *testing.T) {
	src := "stock Foo(x) { new y = x; { new x = y; x = x + 1; } return x; }\n"
	table, _ := buildTable(t, src)

	if len(table.Diagnostics) != 0 {
		t.Fatalf("shadowing in a nested block must not be a redeclaration, got %+v", table.Diagnostics)
	}

	var paramX, blockX symbol.Symbol
	for _, s := range table.Symbols {
		if s.Name != "x" {
			continue
		}
		if s.Kind == symbol.KindParameter {
			paramX = s
		} else {
			blockX = s
		}
	}
	if paramX.ID == 0 || blockX.ID == 0 {
		t.Fatalf("expected two distinct 'x' symbols (parameter and block-local)")
	}
	if paramX.Scope == blockX.Scope {
		t.Errorf("expected the block-local 'x' to live in a different (nested) scope than the parameter")
	}
}

func TestUndefinedSymbol(t *testing.T) {
	src := "stock UseUndefined() { return totallyUndefinedVariable + 1; }\n"
	table, _ := buildTable(t, src)

	undef := table.UndefinedReferences()
	found := false
	for _, r := range undef {
		if r.Name == "totallyUndefinedVariable" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'totallyUndefinedVariable' among UndefinedReferences, got %+v", undef)
	}
}

func TestEnumConstants(t *testing.T) {
	src := "enum E { E_FIRST, E_SECOND, E_THIRD };\nstock Use() { return E_SECOND; }\n"
	table, _ := buildTable(t, src)

	second, ok := findSymbol(table, "E_SECOND")
	if !ok || second.Kind != symbol.KindConstant {
		t.Fatalf("expected E_SECOND to be a constant symbol, got %+v ok=%v", second, ok)
	}
	resolved := false
	for _, ref := range table.References {
		if ref.Name == "E_SECOND" && ref.Resolved == second.ID {
			resolved = true
		}
	}
	if !resolved {
		t.Errorf("expected a reference to E_SECOND to resolve")
	}
}

func TestFunctionKindClassification(t *testing.T) {
	src := "native SetTimer(a, b, c);\nforward OnFoo(a);\npublic OnBar(a) { return 1; }\nstock Baz() { return 1; }\nRegular() { return 1; }\n"
	table, _ := buildTable(t, src)

	want := map[string]symbol.Kind{
		"SetTimer": symbol.KindNative,
		"OnFoo":    symbol.KindForward,
		"OnBar":    symbol.KindPublic,
		"Baz":      symbol.KindStock,
		"Regular":  symbol.KindFunction,
	}
	for name, k := range want {
		sym, ok := findSymbol(table, name)
		if !ok {
			t.Errorf("expected symbol %q to be declared", name)
			continue
		}
		if sym.Kind != k {
			t.Errorf("%s: expected kind %s, got %s", name, k, sym.Kind)
		}
	}
}

func TestForwardAndDefinitionAreCompatible(t *testing.T) {
	table, _ := buildTable(t, "forward OnReady(value);\npublic OnReady(value) { return value; }\n")
	if len(table.Diagnostics) != 0 {
		t.Fatalf("forward and definition must be compatible: %+v", table.Diagnostics)
	}
}

func TestStateImplementationsAreCompatible(t *testing.T) {
	table, _ := buildTable(t, "Handler() <ready> {}\nHandler() <waiting> {}\n")
	if len(table.Diagnostics) != 0 {
		t.Fatalf("state implementations must coexist: %+v", table.Diagnostics)
	}
	var implementations int
	for _, item := range table.Symbols {
		if item.Name == "Handler" && item.StateSelector != "" {
			implementations++
		}
	}
	if implementations != 2 {
		t.Fatalf("state implementations = %d, want 2", implementations)
	}
}

func TestStateNamesAreNotSymbolReferences(t *testing.T) {
	table, _ := buildTable(t, "main() { state traffic:ready; } Handler() <traffic:ready> {}")
	for _, reference := range table.References {
		if reference.Name == "traffic" || reference.Name == "ready" {
			t.Fatalf("state name recorded as symbol reference: %+v", reference)
		}
	}
}

func TestTagsAreNotSymbolReferences(t *testing.T) {
	table, _ := buildTable(t, "main() { new Float:value = 1.0; return _:value + bool:1; }")
	for _, reference := range table.References {
		switch reference.Name {
		case "Float", "_", "bool":
			t.Errorf("tag %q recorded as a symbol reference", reference.Name)
		}
	}
}

func TestReferenceToLaterGlobalResolves(t *testing.T) {
	table, _ := buildTable(t, "stock First() { return Second(); }\nstock Second() { return 1; }\n")
	second, ok := findSymbol(table, "Second")
	if !ok {
		t.Fatal("missing Second symbol")
	}
	for _, ref := range table.References {
		if ref.Name == "Second" && ref.Resolved == second.ID {
			if !ref.IsCall {
				t.Fatal("Second reference was not classified as a call")
			}
			return
		}
	}
	t.Fatal("reference to later global did not resolve")
}

func TestTagExtraction(t *testing.T) {
	src := "stock Float:ComputeRatio() { new Float:total = 10.0; return total; }\n"
	table, _ := buildTable(t, src)

	fn, ok := findSymbol(table, "ComputeRatio")
	if !ok || fn.Tag != "Float" {
		t.Fatalf("expected ComputeRatio tagged Float, got %+v ok=%v", fn, ok)
	}
	v, ok := findSymbol(table, "total")
	if !ok || v.Tag != "Float" {
		t.Fatalf("expected total tagged Float, got %+v ok=%v", v, ok)
	}
}

func TestOperatorOverloadsAndTagUnion(t *testing.T) {
	src := "Float:operator+(Float:left, Float:right) { return left; }\nbool:operator +({bool, Float}:left, bool:right) { return right; }\n"
	table, _ := buildTable(t, src)
	overloads := table.OperatorOverloads("operator+")
	if len(overloads) != 2 || len(table.Diagnostics) != 0 {
		t.Fatalf("overloads=%+v diagnostics=%+v", overloads, table.Diagnostics)
	}
	if overloads[1].ParamTags[0] != "bool|Float" {
		t.Fatalf("union tag=%q", overloads[1].ParamTags[0])
	}
}

func TestArrayParameterAndVariable(t *testing.T) {
	src := "stock Use(arr[10]) { new buf[32]; return arr[0] + buf[0]; }\n"
	table, _ := buildTable(t, src)

	p, ok := findSymbol(table, "arr")
	if !ok || !p.IsArray {
		t.Fatalf("expected 'arr' parameter marked as array, got %+v ok=%v", p, ok)
	}
	v, ok := findSymbol(table, "buf")
	if !ok || !v.IsArray {
		t.Fatalf("expected 'buf' variable marked as array, got %+v ok=%v", v, ok)
	}
}

func TestMalformedSourceDoesNotPanic(t *testing.T) {
	cases := []string{
		"",
		"stock",
		"stock Foo(",
		"new",
		"new x = ",
		"enum {",
		"if (",
		"stock Foo() {",
		"#define X\nstock Foo() { return X; }\n",
	}
	for _, src := range cases {
		src := src
		t.Run("", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on input %q: %v", src, r)
				}
			}()
			table, _ := buildTable(t, src)
			if table == nil {
				t.Fatalf("Build returned nil")
			}
		})
	}
}
