package query

import (
	"context"
	"errors"
	"strings"
	"testing"

	analysis "github.com/pawnkit/pawn-analysis"
	"github.com/pawnkit/pawn-analysis/sema"
	"github.com/pawnkit/pawnkit-core/source"
)

func TestSnapshotCachesUnchangedAnalysis(t *testing.T) {
	uri := source.FileURI("main.pwn")
	snapshot := New(Document{URI: uri, Text: []byte("main() {}"), Version: 1})
	first, err := snapshot.Analyze(context.Background(), uri, analysis.Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := snapshot.Analyze(context.Background(), uri, analysis.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("unchanged analysis was not cached")
	}
}

func TestSnapshotSeparatesPreparedAndCompleteResults(t *testing.T) {
	uri := source.FileURI("main.pwn")
	snapshot := New(Document{URI: uri, Text: []byte("main() { Missing(); }\n"), Version: 1})
	prepared, err := snapshot.Analyze(context.Background(), uri, analysis.Options{
		Names: sema.MapResolver{}, SkipSemantics: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	complete, err := snapshot.Analyze(context.Background(), uri, analysis.Options{
		Names: sema.MapResolver{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared == complete {
		t.Fatal("prepared analysis was reused as a complete result")
	}
	if len(complete.Semantics.Diagnostics) == 0 {
		t.Fatal("complete analysis did not run semantics")
	}
}

func TestSnapshotUpdateInvalidatesChangedDocument(t *testing.T) {
	uri := source.FileURI("main.pwn")
	snapshot := New(Document{URI: uri, Text: []byte("main() {}"), Version: 1})
	first, err := snapshot.Analyze(context.Background(), uri, analysis.Options{})
	if err != nil {
		t.Fatal(err)
	}
	next, ok := snapshot.Update(Document{URI: uri, Text: []byte("main() { return; }"), Version: 2})
	if !ok {
		t.Fatal("update rejected")
	}
	second, err := next.Analyze(context.Background(), uri, analysis.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("changed analysis was reused")
	}
	old, _ := snapshot.Document(uri)
	if old.Version != 1 {
		t.Fatal("update mutated the old snapshot")
	}
}

func TestSnapshotRejectsStaleUpdate(t *testing.T) {
	uri := source.FileURI("main.pwn")
	snapshot := New(Document{URI: uri, Text: []byte("main() {}"), Version: 2})
	next, ok := snapshot.Update(Document{URI: uri, Text: []byte("changed"), Version: 1})
	if ok || next != snapshot {
		t.Fatal("stale update was accepted")
	}
}

func TestSnapshotCopiesDocumentText(t *testing.T) {
	uri := source.FileURI("main.pwn")
	text := []byte("main() {}")
	snapshot := New(Document{URI: uri, Text: text, Version: 1})
	text[0] = 'x'
	document, _ := snapshot.Document(uri)
	if string(document.Text) != "main() {}" {
		t.Fatalf("snapshot text = %q", document.Text)
	}
}

func TestSnapshotCancellationAndMissingDocument(t *testing.T) {
	uri := source.FileURI("main.pwn")
	snapshot := New(Document{URI: uri, Text: []byte("main() {}"), Version: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := snapshot.Analyze(ctx, uri, analysis.Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Analyze() error = %v", err)
	}
	if _, err := snapshot.Analyze(context.Background(), source.FileURI("missing.pwn"), analysis.Options{}); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("Analyze() error = %v", err)
	}
}

func TestAnalyzeWorkspaceResolvesOtherDocuments(t *testing.T) {
	mainURI := source.FileURI("main.pwn")
	helperURI := source.FileURI("helper.inc")
	snapshot := New(
		Document{URI: mainURI, Text: []byte("main() { Helper(1); }"), Version: 1},
		Document{URI: helperURI, Text: []byte("stock Helper(value) { return value; }"), Version: 1},
	)

	result, err := snapshot.AnalyzeWorkspace(context.Background(), analysis.Options{Names: sema.MapResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Files[mainURI].Diagnostics {
		if item.Code == "pawn-analysis:sema/undefined-symbol" {
			t.Fatalf("workspace declaration was unresolved: %+v", item)
		}
	}
}

func TestAnalyzeWorkspaceChecksSharedCallableArity(t *testing.T) {
	mainURI := source.FileURI("main.pwn")
	snapshot := New(
		Document{URI: mainURI, Text: []byte("main() { Helper(); }"), Version: 1},
		Document{URI: source.FileURI("helper.inc"), Text: []byte("stock Helper(value) { return value; }"), Version: 1},
	)

	result, err := snapshot.AnalyzeWorkspace(context.Background(), analysis.Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range result.Files[mainURI].Diagnostics {
		if item.Code == "pawn-analysis:sema/argument-count" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected shared callable arity diagnostic")
	}
}

func TestAnalyzeWorkspaceExpandsCallableTagMacros(t *testing.T) {
	mainURI := source.FileURI("main.pwn")
	snapshot := New(
		Document{URI: mainURI, Text: []byte("main() { Accept(String:1); UseHandle(Handle:1); }"), Version: 1},
		Document{URI: source.FileURI("helper.inc"), Text: []byte("#define AnyTag {_, bool, Float}\n#define HandleTag {Handle}\nnative Accept(AnyTag:value);\nnative UseHandle(HandleTag:value);"), Version: 1},
	)

	result, err := snapshot.AnalyzeWorkspace(context.Background(), analysis.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Files[mainURI].Diagnostics {
		if item.Code == "pawn-analysis:sema/tag-mismatch" {
			t.Fatalf("expanded tag macro rejected a valid argument: %+v", item)
		}
	}
}

func TestAnalyzeDocumentsExcludesUnselectedDeclarations(t *testing.T) {
	mainURI := source.FileURI("main.pwn")
	helperURI := source.FileURI("helper.inc")
	snapshot := New(
		Document{URI: mainURI, Text: []byte("main() { Helper(); Unrelated(); }"), Version: 1},
		Document{URI: helperURI, Text: []byte("stock Helper() {}"), Version: 1},
		Document{URI: source.FileURI("unrelated.inc"), Text: []byte("stock Unrelated() {}"), Version: 1},
	)

	result, err := snapshot.AnalyzeDocuments(
		context.Background(), []source.URI{mainURI, helperURI}, analysis.Options{Names: sema.MapResolver{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var undefined []string
	for _, item := range result.Files[mainURI].Diagnostics {
		if item.Code == "pawn-analysis:sema/undefined-symbol" {
			undefined = append(undefined, item.Message)
		}
	}
	if len(undefined) != 1 || !strings.Contains(undefined[0], "Unrelated") {
		t.Fatalf("undefined diagnostics = %v", undefined)
	}
}
