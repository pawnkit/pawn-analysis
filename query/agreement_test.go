package query

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	analysis "github.com/pawnkit/pawn-analysis"
	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawn-analysis/sema"
	"github.com/pawnkit/pawn-analysis/symbol"
	"github.com/pawnkit/pawn-parser/token"
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

func TestCorpusCleanAndIncrementalAnalysisAgree(t *testing.T) {
	root := queryCorpusRoot()
	if root == "" {
		t.Skip("pawn-corpus is unavailable")
	}
	paths, err := filepath.Glob(filepath.Join(root, "preprocessor", "*.pwn"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".pwn"), func(t *testing.T) {
			finalText, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			uri := source.FileURI(path)
			opts := analysis.Options{RetainExpanded: true, Names: sema.MapResolver{}}

			incremental := New(Document{URI: uri, Text: []byte("main() {}\n"), Version: 1})
			if _, err := incremental.Analyze(context.Background(), uri, opts); err != nil {
				t.Fatal(err)
			}
			incremental, ok := incremental.Update(Document{URI: uri, Text: finalText, Version: 2})
			if !ok {
				t.Fatal("final update rejected")
			}
			got, err := incremental.Analyze(context.Background(), uri, opts)
			if err != nil {
				t.Fatal(err)
			}

			clean := New(Document{URI: uri, Text: finalText, Version: 2})
			want, err := clean.Analyze(context.Background(), uri, opts)
			if err != nil {
				t.Fatal(err)
			}
			assertAnalysisEqual(t, got, want)
		})
	}
}

func TestCleanAndIncrementalWorkspaceAnalysisAgree(t *testing.T) {
	mainURI := source.FileURI("main.pwn")
	helperURI := source.FileURI("helper.inc")
	initial := []Document{
		{URI: mainURI, Text: []byte("main() { return OldValue(); }\n"), Version: 1},
		{URI: helperURI, Text: []byte("stock OldValue() { return 1; }\n"), Version: 1},
	}
	final := []Document{
		{URI: mainURI, Text: []byte("#define CALL(%0) %0()\nmain() { return CALL(NewValue); }\n"), Version: 2},
		{URI: helperURI, Text: []byte("stock NewValue() { return 2; }\n"), Version: 2},
	}
	opts := analysis.Options{RetainExpanded: true, Names: sema.MapResolver{}}

	incremental := New(initial...)
	if _, err := incremental.AnalyzeWorkspace(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	for _, document := range final {
		var ok bool
		incremental, ok = incremental.Update(document)
		if !ok {
			t.Fatalf("update rejected for %s", document.URI)
		}
	}
	got, err := incremental.AnalyzeWorkspace(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	want, err := New(final...).AnalyzeWorkspace(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(summarizeWorkspace(got), summarizeWorkspace(want)) {
		t.Fatalf("incremental workspace differs from clean analysis\ngot:  %#v\nwant: %#v", summarizeWorkspace(got), summarizeWorkspace(want))
	}
}

func TestTriviaReuseMatchesCleanAnalysis(t *testing.T) {
	uri := source.FileURI("main.pwn")
	initial := []byte("stock Work() { return 1; } // old\n")
	final := []byte("stock Work() { return 1; } // new\n")
	opts := analysis.Options{
		RetainExpanded: true,
		Names:          sema.MapResolver{},
		Revision:       "project:1",
	}

	incremental := New(Document{URI: uri, Text: initial, Version: 1})
	if _, err := incremental.Analyze(context.Background(), uri, opts); err != nil {
		t.Fatal(err)
	}
	incremental, ok := incremental.Update(Document{URI: uri, Text: final, Version: 2})
	if !ok {
		t.Fatal("update rejected")
	}
	got, err := incremental.Analyze(context.Background(), uri, opts)
	if err != nil {
		t.Fatal(err)
	}
	want, err := New(Document{URI: uri, Text: final, Version: 2}).Analyze(
		context.Background(), uri, opts,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertAnalysisEqual(t, got, want)
}

func TestResolverRevisionMatchesCleanAnalysis(t *testing.T) {
	uri := source.FileURI("main.pwn")
	text := []byte("#include <value>\nmain() { return IncludedValue(); }\n")
	snapshot := New(Document{URI: uri, Text: text, Version: 1})

	oldOptions := analysis.Options{
		Includes: staticIncludeResolver{"value": "stock OldValue() { return 1; }\n"},
		Revision: "old", RetainExpanded: true,
	}
	if _, err := snapshot.Analyze(context.Background(), uri, oldOptions); err != nil {
		t.Fatal(err)
	}

	newOptions := analysis.Options{
		Includes: staticIncludeResolver{"value": "stock IncludedValue() { return 2; }\n"},
		Revision: "new", RetainExpanded: true,
	}
	got, err := snapshot.Analyze(context.Background(), uri, newOptions)
	if err != nil {
		t.Fatal(err)
	}
	want, err := New(Document{URI: uri, Text: text, Version: 1}).Analyze(context.Background(), uri, newOptions)
	if err != nil {
		t.Fatal(err)
	}
	assertAnalysisEqual(t, got, want)
	if !strings.Contains(string(got.Preprocess.ExpandedSource), "IncludedValue") {
		t.Fatal("resolver revision reused stale include content")
	}
}

func TestIncludedFileRevisionMatchesCleanAnalysis(t *testing.T) {
	root := queryCorpusRoot()
	if root == "" {
		t.Skip("pawn-corpus is unavailable")
	}
	fixtureDir := filepath.Join(root, "preprocessor", "compiler_source_location")
	text, err := os.ReadFile(filepath.Join(fixtureDir, "main.pwn"))
	if err != nil {
		t.Fatal(err)
	}
	finalInclude, err := os.ReadFile(filepath.Join(fixtureDir, "broken.inc"))
	if err != nil {
		t.Fatal(err)
	}

	uri := source.FileURI(filepath.Join(fixtureDir, "main.pwn"))
	snapshot := New(Document{URI: uri, Text: text, Version: 1})
	initial := analysis.Options{
		Includes: preprocess.MapResolver{"broken.inc": []byte("#define INCLUDED_VALUE 1\n")},
		Revision: "include-v1", RetainExpanded: true,
	}
	if _, err := snapshot.Analyze(context.Background(), uri, initial); err != nil {
		t.Fatal(err)
	}

	final := analysis.Options{
		Includes: preprocess.MapResolver{"broken.inc": finalInclude},
		Revision: "include-v2", RetainExpanded: true,
	}
	got, err := snapshot.Analyze(context.Background(), uri, final)
	if err != nil {
		t.Fatal(err)
	}
	want, err := New(Document{URI: uri, Text: text, Version: 1}).Analyze(context.Background(), uri, final)
	if err != nil {
		t.Fatal(err)
	}
	assertAnalysisEqual(t, got, want)
	if len(got.Preprocess.Diagnostics) == 0 {
		t.Fatal("updated include did not report its error directive")
	}
}

func TestPredefinedProfileChangeMatchesCleanAnalysis(t *testing.T) {
	root := queryCorpusRoot()
	if root == "" {
		t.Skip("pawn-corpus is unavailable")
	}
	path := filepath.Join(root, "preprocessor", "profile_openmp_define.pwn")
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	uri := source.FileURI(path)
	snapshot := New(Document{URI: uri, Text: text, Version: 1})
	base := analysis.Options{Revision: "samp-037", RetainExpanded: true}
	if _, err := snapshot.Analyze(context.Background(), uri, base); err != nil {
		t.Fatal(err)
	}

	openMP := analysis.Options{
		Predefined: map[string]string{"__OPEN_MP__": "1"},
		Revision:   "openmp", RetainExpanded: true,
	}
	got, err := snapshot.Analyze(context.Background(), uri, openMP)
	if err != nil {
		t.Fatal(err)
	}
	want, err := New(Document{URI: uri, Text: text, Version: 1}).Analyze(context.Background(), uri, openMP)
	if err != nil {
		t.Fatal(err)
	}
	assertAnalysisEqual(t, got, want)
	for _, item := range got.Diagnostics {
		if item.Severity == diagnostic.SeverityError {
			t.Fatalf("open.mp profile produced an error: %+v", item)
		}
	}
}

type staticIncludeResolver map[string]string

func (r staticIncludeResolver) Resolve(_, path string, _ bool) ([]byte, string, bool) {
	content, ok := r[path]
	return []byte(content), source.FileURI(path + ".inc").String(), ok
}

type analysisSummary struct {
	Expanded       string
	ExpandedTokens []token.Token
	Branches       []preprocess.Branch
	Includes       []preprocess.Include
	Macros         []macroSummary
	PreDiagnostics []preprocess.Diagnostic
	Symbols        []symbol.Symbol
	References     []symbol.Reference
	Unknown        []symbol.Reference
	ControlFlow    []sema.FunctionFlow
	Diagnostics    []diagnostic.Diagnostic
}

type macroSummary struct {
	Name            string
	Kind            preprocess.MacroKind
	ParamCount      int
	ParamSlots      map[int]int
	NamedParams     map[string]int
	FlexiblePattern bool
	File            uint32
	DefSpan         preprocess.ByteRange
}

func summarizeAnalysis(result *analysis.Result) analysisSummary {
	names := make([]string, 0, len(result.Preprocess.Macros))
	for name := range result.Preprocess.Macros {
		names = append(names, name)
	}
	sort.Strings(names)
	macros := make([]macroSummary, 0, len(names))
	for _, name := range names {
		macro := result.Preprocess.Macros[name]
		macros = append(macros, macroSummary{
			Name: name, Kind: macro.Kind, ParamCount: macro.ParamCount,
			ParamSlots: macro.ParamSlots, NamedParams: macro.NamedParams,
			FlexiblePattern: macro.FlexiblePattern, File: macro.File, DefSpan: macro.DefSpan,
		})
	}
	return analysisSummary{
		Expanded:       string(result.Preprocess.ExpandedSource),
		ExpandedTokens: result.Preprocess.ExpandedTokens,
		Branches:       result.Preprocess.Branches,
		Includes:       result.Preprocess.Includes,
		Macros:         macros,
		PreDiagnostics: result.Preprocess.Diagnostics,
		Symbols:        result.Symbols.Symbols,
		References:     result.Symbols.References,
		Unknown:        result.Semantics.Unknown,
		ControlFlow:    result.ControlFlow,
		Diagnostics:    result.Diagnostics,
	}
}

func FuzzPersistentSnapshotMatchesCleanAnalysis(f *testing.F) {
	f.Add([]byte{0, 3, 1, 8, 2, 12, 0, 5})
	f.Add([]byte{2, 0, 2, 1, 2, 2, 1, 3})
	f.Fuzz(func(t *testing.T, operations []byte) {
		if len(operations) > 48 {
			operations = operations[:48]
		}
		uri := source.FileURI("fuzz.pwn")
		opts := analysis.Options{Revision: "project:1"}
		buffer := source.NewTextBuffer([]byte("stock Add(value) { return value + 1; }\nmain() { return Add(2); }\n"))
		snapshot := New(Document{URI: uri, Buffer: buffer, Version: 1})
		if _, err := snapshot.Analyze(context.Background(), uri, opts); err != nil {
			t.Fatal(err)
		}

		version := int64(1)
		replacements := [...]string{"x", " ", ";", "\n", "0"}
		for index := 0; index+1 < len(operations); index += 2 {
			position := int(operations[index]) % (buffer.Len() + 1)
			end := position
			replacement := replacements[int(operations[index+1])%len(replacements)]
			switch operations[index] % 3 {
			case 1:
				if position < buffer.Len() {
					end = position + 1
				}
				replacement = ""
			case 2:
				if position < buffer.Len() {
					end = position + 1
				}
			}

			var err error
			buffer, err = buffer.Apply(source.Offset(position), source.Offset(end), replacement)
			if err != nil {
				t.Fatal(err)
			}
			version++
			next, accepted := snapshot.UpdateOwned(Document{URI: uri, Buffer: buffer, Version: version})
			if !accepted {
				t.Fatal("snapshot rejected a new revision")
			}
			got, err := next.Analyze(context.Background(), uri, opts)
			if err != nil {
				t.Fatal(err)
			}
			want, err := New(Document{URI: uri, Buffer: buffer, Version: version}).Analyze(
				context.Background(), uri, opts,
			)
			if err != nil {
				t.Fatal(err)
			}
			assertAnalysisEqual(t, got, want)
			snapshot = next
		}
	})
}

func summarizeWorkspace(result *WorkspaceResult) []analysisSummary {
	uris := make([]source.URI, 0, len(result.Files))
	for uri := range result.Files {
		uris = append(uris, uri)
	}
	sort.Slice(uris, func(i, j int) bool { return uris[i].String() < uris[j].String() })
	summaries := make([]analysisSummary, 0, len(uris))
	for _, uri := range uris {
		summaries = append(summaries, summarizeAnalysis(result.Files[uri]))
	}
	return summaries
}

func assertAnalysisEqual(t *testing.T, got, want *analysis.Result) {
	t.Helper()
	gotSummary := summarizeAnalysis(got)
	wantSummary := summarizeAnalysis(want)
	if !reflect.DeepEqual(gotSummary, wantSummary) {
		t.Fatalf("incremental analysis differs from clean analysis\ngot:  %#v\nwant: %#v", gotSummary, wantSummary)
	}
}

func queryCorpusRoot() string {
	if root := os.Getenv("PAWN_CORPUS_DIR"); root != "" {
		if filepath.IsAbs(root) {
			return root
		}
		root = filepath.Join("..", root)
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
		return ""
	}
	root := filepath.Join("..", "..", "pawn-corpus")
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return ""
}
