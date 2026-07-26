package analysis_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	analysis "github.com/pawnkit/pawn-analysis"
	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawn-analysis/sema"
	"github.com/pawnkit/pawn-analysis/symbol"
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

var (
	_ func([]byte, analysis.Options) *analysis.Result                           = analysis.Analyze
	_ func(context.Context, []byte, analysis.Options) (*analysis.Result, error) = analysis.AnalyzeContext
	_ func([]byte, preprocess.Options) *preprocess.Result                       = preprocess.Run
	_ symbol.ID                                                                 = 0
	_ diagnostic.Diagnostic
	_ source.Span
)

var _ = func(result *analysis.Result) {
	_ = result.Diagnostics
	_ = result.Symbols
	_ = result.Semantics
	_ = result.Preprocess
}

func TestCompilerCompatibilityCases(t *testing.T) {
	tests := []struct {
		name string
		text string
		code string
	}{
		{"forward and definition", "forward Helper(value); Helper(value) {}", ""},
		{"later global", "main() { value = 1; } new value;", ""},
		{"non-callable", "main() { new value; value(); }", "pawn-analysis:sema/not-callable"},
		{"argument count", "Helper(a, b) {} main() { Helper(1); }", "pawn-analysis:sema/argument-count"},
		{"tag assignment", "main() { new Float:value; new bool:other; value = other; }", "pawn-analysis:sema/tag-mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := analysis.Analyze([]byte(test.text), analysis.Options{Names: sema.MapResolver{}})
			found := false
			for _, item := range result.Diagnostics {
				found = found || item.Code == test.code
			}
			if test.code != "" && !found {
				t.Fatalf("missing %s: %+v", test.code, result.Diagnostics)
			}
			if test.code == "" && len(result.Diagnostics) != 0 {
				t.Fatalf("unexpected diagnostics: %+v", result.Diagnostics)
			}
		})
	}
}

func TestExternalCorpus(t *testing.T) {
	root := corpusRoot()
	if root == "" {
		t.Skip("PAWN_CORPUS_DIR is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".pwn") && !strings.HasSuffix(path, ".inc") {
			return nil
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := analysis.AnalyzeContext(ctx, text, analysis.Options{URI: source.FileURI(path)}); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("corpus contains no Pawn sources")
	}
}

func TestCorpusSemanticDiagnostics(t *testing.T) {
	root := corpusRoot()
	if root == "" {
		t.Skip("pawn-corpus is unavailable")
	}
	classes := map[string]string{
		"semantic/duplicate-declaration":        "pawn-analysis:symbol/redeclared",
		"semantic/tag-mismatch":                 "pawn-analysis:sema/tag-mismatch",
		"semantic/undefined-symbol":             "pawn-analysis:sema/undefined-symbol",
		"semantic/unreachable-code":             "pawn-analysis:sema/unreachable",
		"semantic/unknown-state":                "pawn-analysis:sema/unknown-state",
		"semantic/unknown-automaton":            "pawn-analysis:sema/unknown-automaton",
		"semantic/state-conflict":               "pawn-analysis:sema/state-conflict",
		"semantic/automaton-conflict":           "pawn-analysis:sema/automaton-conflict",
		"semantic/missing-state-implementation": "pawn-analysis:sema/missing-state-implementation",
		"semantic/invalid-state-function":       "pawn-analysis:sema/invalid-state-function",
		"semantic/no-defined-states":            "pawn-analysis:sema/no-defined-states",
		"semantic/invalid-state-variable":       "pawn-analysis:sema/invalid-state-variable",
		"semantic/state-variable-shadow":        "pawn-analysis:sema/state-variable-shadow",
		"semantic/forward-state-ignored":        "pawn-analysis:sema/forward-state-ignored",
		"semantic/initialized-state-variable":   "pawn-analysis:sema/initialized-state-variable",
		"semantic/constant-before-declaration":  "pawn-analysis:sema/constant-before-declaration",
	}
	metadata, err := filepath.Glob(filepath.Join(root, "semantics", "*.pwn.meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, metaPath := range metadata {
		meta := readCorpusMetadata(t, metaPath)
		if meta.Expected.Result == "pending" {
			continue
		}
		if meta.Expected.Result == "valid" {
			t.Run(meta.ID, func(t *testing.T) {
				path := strings.TrimSuffix(metaPath, ".meta.json")
				text, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				result := analysis.Analyze(text, analysis.Options{URI: source.FileURI(path), Names: sema.MapResolver{}})
				for _, item := range result.Diagnostics {
					if item.Severity == diagnostic.SeverityError {
						t.Errorf("unexpected error: %+v", item)
					}
				}
			})
			continue
		}
		code, supported := classes[meta.Expected.DiagnosticClass]
		if !supported {
			continue
		}
		t.Run(meta.ID, func(t *testing.T) {
			path := strings.TrimSuffix(metaPath, ".meta.json")
			text, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			result := analysis.Analyze(text, analysis.Options{URI: source.FileURI(path), Names: sema.MapResolver{}})
			var matching []diagnostic.Diagnostic
			for _, item := range result.Diagnostics {
				if item.Code != code {
					continue
				}
				matching = append(matching, item)
			}
			if len(matching) != len(meta.Expected.Diagnostics) {
				t.Fatalf("%s diagnostics = %d, want %d: %+v", code, len(matching), len(meta.Expected.Diagnostics), result.Diagnostics)
			}
			for i, expected := range meta.Expected.Diagnostics {
				item := matching[i]
				if expected.Severity != "" && item.Severity.String() != expected.Severity {
					t.Errorf("%s severity = %s, want %s", code, item.Severity, expected.Severity)
				}
				if expected.Message != "" && normalizeDiagnosticMessage(item.Message) != normalizeDiagnosticMessage(expected.Message) {
					t.Errorf("%s message = %q, want %q", code, item.Message, expected.Message)
				}
				if expected.Line > 0 {
					line := bytes.Count(text[:int(item.Primary.Start)], []byte{'\n'}) + 1
					if line != expected.Line {
						t.Errorf("%s line = %d, want %d", code, line, expected.Line)
					}
				}
			}
		})
	}
}

func TestCorpusPreprocessorDiagnostics(t *testing.T) {
	root := corpusRoot()
	if root == "" {
		t.Skip("pawn-corpus is unavailable")
	}
	metadata, err := filepath.Glob(filepath.Join(root, "preprocessor", "*.pwn.meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, metaPath := range metadata {
		meta := readCorpusMetadata(t, metaPath)
		if meta.Expected.Result == "pending" {
			continue
		}
		t.Run(meta.ID, func(t *testing.T) {
			path := strings.TrimSuffix(metaPath, ".meta.json")
			text, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			opts := analysis.Options{URI: source.FileURI(path), Names: sema.MapResolver{}, RetainExpanded: true}
			if len(meta.Profiles) == 1 && meta.Profiles[0] == "openmp" {
				opts.Predefined = map[string]string{"__OPEN_MP__": "1"}
			}
			result := analysis.Analyze(text, opts)
			if meta.Expected.Result == "valid" {
				for _, item := range result.Diagnostics {
					if item.Severity == diagnostic.SeverityError {
						t.Errorf("unexpected error: %+v", item)
					}
				}
				return
			}

			code := "pawn-analysis:" + meta.Expected.DiagnosticClass
			var matching []diagnostic.Diagnostic
			for _, item := range result.Diagnostics {
				if item.Code == code {
					matching = append(matching, item)
				}
			}
			if len(matching) != len(meta.Expected.Diagnostics) {
				t.Fatalf("%s diagnostics = %d, want %d: %+v", code, len(matching), len(meta.Expected.Diagnostics), result.Diagnostics)
			}
			for i, expected := range meta.Expected.Diagnostics {
				item := matching[i]
				if expected.Severity != "" && item.Severity.String() != expected.Severity {
					t.Errorf("%s severity = %s, want %s", code, item.Severity, expected.Severity)
				}
				if expected.Message != "" && normalizeDiagnosticMessage(item.Message) != normalizeDiagnosticMessage(expected.Message) {
					t.Errorf("%s message = %q, want %q", code, item.Message, expected.Message)
				}
				if expected.Line > 0 {
					line := bytes.Count(text[:int(item.Primary.Start)], []byte{'\n'}) + 1
					if line != expected.Line {
						t.Errorf("%s line = %d, want %d", code, line, expected.Line)
					}
				}
			}
		})
	}
}

func TestCorpusProjectIncludes(t *testing.T) {
	root := corpusRoot()
	if root == "" {
		t.Skip("pawn-corpus is unavailable")
	}
	projectDir := filepath.Join(root, "projects", "filterscript-with-includes")
	raw, err := os.ReadFile(filepath.Join(projectDir, "pawn.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Entry   string `json:"entry"`
		PawnKit struct {
			IncludePaths []string `json:"includePaths"`
		} `json:"pawnkit"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(projectDir, filepath.FromSlash(manifest.Entry))
	text, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	includePaths := make([]string, len(manifest.PawnKit.IncludePaths))
	for i, path := range manifest.PawnKit.IncludePaths {
		includePaths[i] = filepath.Join(projectDir, filepath.FromSlash(path))
	}
	result := analysis.Analyze(text, analysis.Options{
		URI: source.FileURI(entry), Includes: directoryResolver{includePaths: includePaths}, Names: sema.MapResolver{},
	})
	for _, item := range result.Diagnostics {
		if item.Code == "pawn-analysis:"+string(preprocess.CodeIncludeNotFound) || item.Code == "pawn-analysis:sema/undefined-symbol" {
			t.Fatalf("project fixture failed: %+v", item)
		}
	}
	if len(result.Preprocess.Includes) != 2 {
		t.Fatalf("resolved includes = %d, want 2", len(result.Preprocess.Includes))
	}
}

type corpusMetadata struct {
	ID       string   `json:"id"`
	Profiles []string `json:"profiles"`
	Expected struct {
		Result          string `json:"result"`
		DiagnosticClass string `json:"diagnosticClass"`
		Diagnostics     []struct {
			Severity string `json:"severity"`
			Message  string `json:"message"`
			Line     int    `json:"line"`
		} `json:"diagnostics"`
	} `json:"expected"`
}

func normalizeDiagnosticMessage(message string) string {
	return strings.ReplaceAll(message, `"`, `'`)
}

func readCorpusMetadata(t *testing.T, path string) corpusMetadata {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var meta corpusMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

type directoryResolver struct {
	includePaths []string
}

func (r directoryResolver) Resolve(fromURI, path string, angle bool) ([]byte, string, bool) {
	var candidates []string
	names := []string{filepath.FromSlash(path)}
	if filepath.Ext(path) == "" {
		names = append(names, filepath.FromSlash(path)+".inc")
	}
	if !angle {
		if from, err := source.URI(fromURI).Filename(); err == nil {
			for _, name := range names {
				candidates = append(candidates, filepath.Join(filepath.Dir(from), name))
			}
		}
	}
	for _, root := range r.includePaths {
		for _, name := range names {
			candidates = append(candidates, filepath.Join(root, name))
		}
	}
	for _, candidate := range candidates {
		content, err := os.ReadFile(candidate)
		if err == nil {
			return content, source.FileURI(candidate).String(), true
		}
	}
	return nil, "", false
}

func corpusRoot() string {
	if root := os.Getenv("PAWN_CORPUS_DIR"); root != "" {
		return root
	}
	root := filepath.Join("..", "pawn-corpus")
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return ""
}
