package preprocess

import (
	"context"

	"github.com/pawnkit/pawn-parser/lexer"
	"github.com/pawnkit/pawn-parser/token"
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

// Options controls one preprocessing run. The zero value is usable; all
// limits fall back to conservative, documented defaults.
type Options struct {
	// URI identifies the root file for include resolution and diagnostics.
	URI string
	// Resolver resolves #include/#tryinclude targets. A nil Resolver means
	// includes are recorded but never expanded (Include.Resolved stays
	// false), which is the honest answer when no project context is
	// available rather than guessing at file contents.
	Resolver IncludeResolver
	// Predefined seeds the macro table before processing begins (e.g. a
	// profile's built-in defines such as OPEN_MP). Values are parsed as a
	// single macro-body token run; an empty value defines an empty macro.
	Predefined map[string]string
	TokenCache *TokenCache

	MaxExpansionDepth   int
	MaxConditionalDepth int
	MaxIncludeDepth     int
	MaxOutputTokens     int
}

const (
	defaultMaxExpansionDepth   = 64
	defaultMaxConditionalDepth = 256
	defaultMaxIncludeDepth     = 32
	defaultMaxOutputTokens     = 2_000_000
)

func (o Options) resolved() Options {
	if o.MaxExpansionDepth <= 0 {
		o.MaxExpansionDepth = defaultMaxExpansionDepth
	}
	if o.MaxConditionalDepth <= 0 {
		o.MaxConditionalDepth = defaultMaxConditionalDepth
	}
	if o.MaxIncludeDepth <= 0 {
		o.MaxIncludeDepth = defaultMaxIncludeDepth
	}
	if o.MaxOutputTokens <= 0 {
		o.MaxOutputTokens = defaultMaxOutputTokens
	}
	return o
}

// DirectiveKind identifies which conditional-compilation directive opened a
// [Branch].
type DirectiveKind uint8

const (
	DirectiveIf DirectiveKind = iota + 1
	DirectiveElseif
	DirectiveElse
)

func (k DirectiveKind) String() string {
	switch k {
	case DirectiveIf:
		return "if"
	case DirectiveElseif:
		return "elseif"
	case DirectiveElse:
		return "else"
	default:
		return "unknown"
	}
}

// Branch is one #if/#elseif/#else region, preserving both its own extent
// and whether it was selected, so callers can reconstruct active and
// inactive views of the original source without pawn-analysis discarding
// anything.
type Branch struct {
	File          uint32
	Directive     DirectiveKind
	Depth         int
	DirectiveSpan ByteRange
	ConditionSpan ByteRange // zero for #else
	BodySpan      ByteRange
	Active        bool
	Evaluated     bool // false when short-circuited by an inactive parent
}

// FileInfo describes one file (root or spliced #include) contributing
// tokens to a Result.
type FileInfo struct {
	URI     string
	Depth   int
	Content []byte
	Tokens  []token.Token
}

// Include records one #include/#tryinclude directive and its resolution
// outcome.
type Include struct {
	File          uint32
	Path          string
	Angle         bool // <path> vs "path"
	Optional      bool // #tryinclude
	DirectiveSpan ByteRange
	Active        bool
	Resolved      bool
	ResolvedURI   string
	ChildFile     uint32
	HasChildFile  bool
}

// MacroInvocation records a source range expanded as a macro.
type MacroInvocation struct {
	File  uint32
	Range ByteRange
}

// Result is the immutable outcome of one [Run]. All slices are safe to
// retain; nothing here is mutated after Run returns.
//
// Source and ExpandedSource are deliberately separate buffers: Source is
// the root file's exact bytes (what OriginalTokens and Branches/Includes
// with File == 0 index into), while ExpandedSource is a synthesized buffer
// holding the spelled text of every expanded token in emission order. A
// single expanded token's spelling may come from the macro-definition site,
// a call-site argument, or a spliced #include file - three different
// original buffers - so ExpandedTokens cannot index into Source directly;
// use each token's Origin chain (via github.com/pawnkit/pawn-parser's
// SyntaxToken.Origin) to recover the true original location instead.
type Result struct {
	Files            []FileInfo // Files[0] is the root file.
	Source           []byte
	ExpandedSource   []byte
	OriginalTokens   []token.Token
	ExpandedTokens   []token.Token
	Branches         []Branch
	Includes         []Include
	MacroInvocations []MacroInvocation
	Macros           map[string]Macro
	Diagnostics      []Diagnostic
	Truncated        bool
}

// ToCoreDiagnostics maps all diagnostics to one file.
// Use ToRegistryDiagnostics when include locations matter.
func (r *Result) ToCoreDiagnostics(root source.FileID) []diagnostic.Diagnostic {
	out := make([]diagnostic.Diagnostic, len(r.Diagnostics))
	for i, d := range r.Diagnostics {
		out[i] = d.ToCore(root)
	}
	return out
}

// ToRegistryDiagnostics maps diagnostics through Result.Files.
func (r *Result) ToRegistryDiagnostics(registry *source.Registry, fallback source.FileID) []diagnostic.Diagnostic {
	out := make([]diagnostic.Diagnostic, len(r.Diagnostics))
	for i, item := range r.Diagnostics {
		file := fallback
		if int(item.File) < len(r.Files) && registry != nil {
			uri := source.URI(r.Files[item.File].URI)
			if uri.IsValid() {
				file = registry.Intern(uri)
			}
		}
		out[i] = item.ToCore(file)
	}
	return out
}

func endsLine(t token.Token) bool {
	for _, tr := range t.TrailingTrivia {
		if tr.Kind == token.Newline {
			return true
		}
	}
	return false
}

type directiveKeyword int

const (
	dirUnknown directiveKeyword = iota
	dirInclude
	dirTryInclude
	dirDefine
	dirUndef
	dirIf
	dirElseif
	dirElse
	dirEndif
	dirPragma
	dirError
	dirWarning
	dirAssert
	dirLine
	dirFile
	dirEndinput
	dirEmit
)

func classifyDirective(name string) directiveKeyword {
	switch name {
	case "include":
		return dirInclude
	case "tryinclude":
		return dirTryInclude
	case "define":
		return dirDefine
	case "undef":
		return dirUndef
	case "if":
		return dirIf
	case "elseif", "elif":
		return dirElseif
	case "else":
		return dirElse
	case "endif":
		return dirEndif
	case "pragma":
		return dirPragma
	case "error":
		return dirError
	case "warning":
		return dirWarning
	case "assert":
		return dirAssert
	case "line":
		return dirLine
	case "file":
		return dirFile
	case "endinput":
		return dirEndinput
	case "emit":
		return dirEmit
	default:
		return dirUnknown
	}
}

type condFrame struct {
	parentActive    bool
	branchActive    bool
	taken           bool
	openBranchIndex int
	overflow        bool
}

type frame struct {
	fileIndex uint32
	source    []byte
	toks      []token.Token
	pos       int
	lineStart bool
	condStack []condFrame
	depth     int
	uri       string
	endinput  bool // set by #endinput; stops only this frame, not the whole run.
}

func (f *frame) atEnd() bool { return f.toks[f.pos].Kind == token.EOF }
func (f *frame) cur() token.Token {
	return f.toks[f.pos]
}
func (f *frame) at(k token.Kind) bool { return f.cur().Kind == k }

func (f *frame) advance() token.Token {
	t := f.toks[f.pos]
	if f.pos < len(f.toks)-1 {
		f.pos++
	}
	f.lineStart = endsLine(t)
	return t
}

func (f *frame) currentActive() bool {
	if len(f.condStack) == 0 {
		return true
	}
	return f.condStack[len(f.condStack)-1].branchActive
}

type hideSet map[string]bool

func (h hideSet) with(name string) hideSet {
	next := make(hideSet, len(h)+1)
	for k := range h {
		next[k] = true
	}
	next[name] = true
	return next
}

type engine struct {
	ctx          context.Context
	cancellable  bool
	cancelled    error
	steps        uint32
	macros       *macroTable
	out          []token.Token
	expandedBuf  []byte
	branches     []Branch
	includes     []Include
	invocations  []MacroInvocation
	files        []FileInfo
	diags        []Diagnostic
	opts         Options
	outputTokens int
	truncated    bool
	stopped      bool
	includeStack map[string]int

	depthLimitWarned bool
}

// Run preprocesses src and returns the resulting three-view [Result]. Run
// never panics on malformed input; unbalanced or truncated constructs are
// reported as diagnostics and bounded by Options' limits.
func Run(src []byte, opts Options) *Result {
	result, _ := run(src, opts, context.Background(), false)
	return result
}

// RunContext preprocesses src and stops when ctx is cancelled.
func RunContext(ctx context.Context, src []byte, opts Options) (*Result, error) {
	return run(src, opts, ctx, true)
}

func run(src []byte, opts Options, ctx context.Context, cancellable bool) (*Result, error) {
	if cancellable {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	opts = opts.resolved()
	e := &engine{
		ctx:          ctx,
		cancellable:  cancellable,
		macros:       newMacroTable(),
		opts:         opts,
		includeStack: make(map[string]int),
		files:        []FileInfo{{URI: opts.URI, Content: src}},
	}
	for name, value := range opts.Predefined {
		e.macros.define(Macro{
			Name: name, Kind: MacroObjectLike, Body: retainTokenOrigins(tokenizeBody(value)),
		})
	}

	var originalTokens []token.Token
	var err error
	if cancellable {
		originalTokens, err = lexer.TokenizeContext(ctx, src)
	} else {
		originalTokens = lexer.Tokenize(src)
	}
	if err != nil {
		return nil, err
	}
	e.files[0].Tokens = originalTokens
	outputCapacity := min(len(originalTokens), opts.MaxOutputTokens+1)
	e.out = make([]token.Token, 0, outputCapacity)
	e.expandedBuf = make([]byte, 0, len(src))
	root := &frame{fileIndex: 0, source: src, toks: originalTokens, uri: opts.URI, lineStart: true}
	e.run(root)
	if e.cancelled != nil {
		return nil, e.cancelled
	}
	e.appendEOF()
	e.backfillPositions()

	return &Result{
		Files:            e.files,
		Source:           src,
		ExpandedSource:   e.expandedBuf,
		OriginalTokens:   originalTokens,
		ExpandedTokens:   e.out,
		Branches:         e.branches,
		Includes:         e.includes,
		MacroInvocations: e.invocations,
		Macros:           e.macros.snapshot(),
		Diagnostics:      e.diags,
		Truncated:        e.truncated,
	}, nil
}

func (e *engine) pollCancellation() bool {
	if !e.cancellable {
		return false
	}
	e.steps++
	if e.steps%256 != 0 {
		return false
	}
	if err := e.ctx.Err(); err != nil {
		e.cancelled = err
		e.stopped = true
		return true
	}
	return false
}

func (e *engine) appendEOF() {
	if n := len(e.out); n > 0 && e.out[n-1].Kind == token.EOF {
		return
	}
	end := token.Position{Offset: len(e.expandedBuf)}
	e.out = append(e.out, token.Token{Kind: token.EOF, Start: end, End: end})
}

// backfillPositions computes Line/Col for every expanded token's Start/End
// from the final synthesized buffer, now that its length is fixed.
func (e *engine) backfillPositions() {
	lm := token.NewLineMap(e.expandedBuf)
	for i := range e.out {
		e.out[i].Start = lm.Position(uint32(e.out[i].Start.Offset)) //nolint:gosec // bounded by expandedBuf length.
		e.out[i].End = lm.Position(uint32(e.out[i].End.Offset))     //nolint:gosec // bounded by expandedBuf length.
	}
}

func (e *engine) run(f *frame) {
	for !f.atEnd() && !e.stopped && !f.endinput {
		if e.pollCancellation() {
			return
		}
		if f.lineStart && f.cur().Kind == token.Hash {
			e.handleDirectiveLine(f)
			continue
		}
		e.emitActive(f)
	}
	if len(f.condStack) > 0 && !f.endinput && !e.stopped {
		last := f.toks[f.pos]
		e.diag(f, CodeUnterminatedConditional, diagnostic.SeverityError,
			"unterminated conditional: missing #endif", spanOf(last, last))
	}
}
