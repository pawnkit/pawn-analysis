package preprocess

import (
	"github.com/pawnkit/pawn-parser/token"
	"github.com/pawnkit/pawnkit-core/diagnostic"
)

func (e *engine) emitActive(f *frame) {
	tok := f.advance()
	if !f.currentActive() {
		return
	}
	if tok.Kind == token.Identifier {
		name := tok.Text(f.source)
		if m, ok := e.macros.lookup(name); ok {
			switch m.Kind {
			case MacroObjectLike:
				e.expandObjectAt(f, tok, m)
				return
			case MacroFunctionLike:
				if f.cur().Kind == token.LParen {
					e.expandFunctionAt(f, tok, m)
					return
				}
			}
		}
	}
	e.appendOut(toPtok(f.source, tok, f.fileIndex))
}

// appendOut writes a token into the synthesized output buffer.
func (e *engine) appendOut(t ptok) {
	if e.truncated {
		return
	}
	e.outputTokens++
	if e.outputTokens > e.opts.MaxOutputTokens {
		e.truncated = true
		e.stopped = true
		e.diags = append(e.diags, Diagnostic{
			Code: CodeOutputSizeLimit, Severity: diagnostic.SeverityError,
			Message: "preprocessor output size limit exceeded",
			Range:   ByteRange{Start: t.Start.Offset, End: t.End.Offset},
		})
		return
	}
	if len(e.expandedBuf) > 0 {
		e.expandedBuf = append(e.expandedBuf, ' ')
	}
	start := len(e.expandedBuf)
	e.expandedBuf = append(e.expandedBuf, t.text...)
	out := t.Token
	if out.Origin == nil {
		out.Origin = &token.Origin{
			Span: token.Span{File: t.file, Start: out.Start, End: out.End},
		}
	}
	out.Start = token.Position{Offset: start}
	out.End = token.Position{Offset: start + len(t.text)}
	out.LeadingTrivia = nil
	out.TrailingTrivia = nil
	e.out = append(e.out, out)
}

func (e *engine) appendAllOut(toks []ptok) {
	for _, t := range toks {
		e.appendOut(t)
		if e.truncated {
			return
		}
	}
}

func wrapOrigin(t ptok, inv token.Span, macroName string) ptok {
	parent := t.Origin
	if parent == nil {
		parent = &token.Origin{Span: token.Span{File: t.file, Start: t.Start, End: t.End}}
	}
	t.Origin = &token.Origin{Span: inv, Macro: macroName, Parent: parent}
	return t
}

func invocationSpan(file uint32, start, end token.Token) token.Span {
	return token.Span{File: file, Start: start.Start, End: end.End}
}

func nestedInvocationSpan(start, end ptok) token.Span {
	if start.Origin != nil {
		return start.Origin.Span
	}
	return token.Span{File: start.file, Start: start.Start, End: end.End}
}

func (e *engine) expandObjectAt(f *frame, tok token.Token, m Macro) {
	inv := invocationSpan(f.fileIndex, tok, tok)
	body := make([]ptok, len(m.Body))
	for i, bt := range m.Body {
		body[i] = wrapOrigin(bt, inv, m.Name)
	}
	result := e.expandRun(f, body, hideSet{}.with(m.Name), 1)
	e.appendAllOut(result)
}

func (e *engine) expandFunctionAt(f *frame, tok token.Token, m Macro) {
	f.advance() // '('
	args, closeParen, ok := e.collectArgs(f)
	if !ok {
		if f.currentActive() {
			e.diag(f, CodeUnterminatedInvocation, diagnostic.SeverityError,
				"unterminated invocation of macro '"+m.Name+"'", spanOf(tok, tok))
		}
		e.appendOut(toPtok(f.source, tok, f.fileIndex))
		return
	}
	if len(args) != m.ParamCount && f.currentActive() {
		e.diag(f, CodeMacroArgumentMismatch, diagnostic.SeverityWarning,
			"macro invocation argument count mismatch", spanOf(tok, closeParen))
	}
	inv := invocationSpan(f.fileIndex, tok, closeParen)
	body := substituteParams(m, args, inv)
	result := e.expandRun(f, body, hideSet{}.with(m.Name), 1)
	e.appendAllOut(result)
}

// collectArgs scans comma-separated argument token runs starting right
// after an already-consumed '(', respecting nested (), [], {} depth, up to
// and including the matching ')'. A single empty argument list "()"
// produces zero arguments.
func (e *engine) collectArgs(f *frame) (args [][]ptok, closeParen token.Token, ok bool) {
	if f.cur().Kind == token.RParen {
		return nil, f.advance(), true
	}
	depth := 0
	var current []ptok
	for {
		if f.atEnd() {
			return nil, token.Token{}, false
		}
		t := f.cur()
		switch t.Kind {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RBracket, token.RBrace:
			depth--
		case token.RParen:
			if depth == 0 {
				f.advance()
				args = append(args, current)
				return args, t, true
			}
			depth--
		case token.Comma:
			if depth == 0 {
				f.advance()
				args = append(args, current)
				current = nil
				continue
			}
		}
		current = append(current, toPtok(f.source, t, f.fileIndex))
		f.advance()
	}
}

func substituteParams(m Macro, args [][]ptok, inv token.Span) []ptok {
	var out []ptok
	for _, bt := range m.Body {
		if bt.Kind == token.MacroParam {
			if idx, isParam := parseParamIndex(bt.text); isParam {
				if idx < len(args) {
					for _, at := range args[idx] {
						out = append(out, wrapOrigin(at, inv, m.Name))
					}
				}
				continue
			}
			out = append(out, wrapOrigin(bt, inv, m.Name))
			continue
		}
		if bt.Kind == token.Identifier {
			if idx, named := m.NamedParams[bt.text]; named {
				if idx < len(args) {
					for _, at := range args[idx] {
						out = append(out, wrapOrigin(at, inv, m.Name))
					}
				}
				continue
			}
		}
		out = append(out, wrapOrigin(bt, inv, m.Name))
	}
	return out
}

func (e *engine) expandRun(f *frame, toks []ptok, hide hideSet, depth int) []ptok {
	if depth > e.opts.MaxExpansionDepth {
		e.truncated = true
		if !e.depthLimitWarned {
			e.depthLimitWarned = true
			r := ByteRange{}
			if len(toks) > 0 {
				r = ByteRange{Start: toks[0].Start.Offset, End: toks[len(toks)-1].End.Offset}
			}
			e.diags = append(e.diags, Diagnostic{
				File: f.fileIndex, Code: CodeExpansionDepthLimit, Severity: diagnostic.SeverityError,
				Message: "macro expansion depth limit exceeded", Range: r,
			})
		}
		return toks
	}
	var out []ptok
	i := 0
	for i < len(toks) {
		// Stop recursive expansion before it builds an oversized slice.
		if len(out) > e.opts.MaxOutputTokens {
			e.truncated = true
			if !e.sizeLimitWarned {
				e.sizeLimitWarned = true
				e.diags = append(e.diags, Diagnostic{
					File: f.fileIndex, Code: CodeOutputSizeLimit, Severity: diagnostic.SeverityError,
					Message: "macro expansion output size limit exceeded", Range: spanOf(toks[0].Token, toks[len(toks)-1].Token),
				})
			}
			break
		}
		t := toks[i]
		if t.Kind == token.Identifier && !hide[t.text] {
			if m, ok := e.macros.lookup(t.text); ok {
				switch m.Kind {
				case MacroObjectLike:
					inv := nestedInvocationSpan(t, t)
					body := make([]ptok, len(m.Body))
					for j, bt := range m.Body {
						body[j] = wrapOrigin(bt, inv, m.Name)
					}
					out = append(out, e.expandRun(f, body, hide.with(m.Name), depth+1)...)
					i++
					continue
				case MacroFunctionLike:
					if i+1 < len(toks) && toks[i+1].Kind == token.LParen {
						args, endIdx, ok := collectArgsSlice(toks, i+2)
						if ok {
							inv := nestedInvocationSpan(t, toks[endIdx-1])
							sub := substituteParams(m, args, inv)
							out = append(out, e.expandRun(f, sub, hide.with(m.Name), depth+1)...)
							i = endIdx
							continue
						}
					}
				}
			}
		}
		out = append(out, t)
		i++
	}
	return out
}

// collectArgsSlice mirrors engine.collectArgs but operates over a detached
// token slice (used when rescanning an already-substituted macro body)
// rather than a live frame cursor.
func collectArgsSlice(toks []ptok, start int) (args [][]ptok, next int, ok bool) {
	i := start
	if i >= len(toks) {
		return nil, 0, false
	}
	if toks[i].Kind == token.RParen {
		return nil, i + 1, true
	}
	depth := 0
	var current []ptok
	for i < len(toks) {
		t := toks[i]
		switch t.Kind {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RBracket, token.RBrace:
			depth--
		case token.RParen:
			if depth == 0 {
				args = append(args, current)
				return args, i + 1, true
			}
			depth--
		case token.Comma:
			if depth == 0 {
				args = append(args, current)
				current = nil
				i++
				continue
			}
		}
		current = append(current, t)
		i++
	}
	return nil, 0, false
}
