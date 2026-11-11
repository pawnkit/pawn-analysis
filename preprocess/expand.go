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
			if m.pattern != nil {
				if !e.expandPatternAt(f, tok, m) {
					e.appendOut(toPtok(f.source, tok, f.fileIndex))
				}
				return
			}
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
	if e.truncated || e.pollCancellation() {
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
	if e.condition != nil {
		e.condition.tokens = append(e.condition.tokens, t)
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
	e.reserveOutputToken()
	e.out = append(e.out, out)
}

func (e *engine) reserveOutputToken() {
	if len(e.out) < cap(e.out) {
		return
	}
	limit := e.opts.MaxOutputTokens + 1
	capacity := max(64, cap(e.out)*2)
	if capacity > limit {
		capacity = limit
	}
	next := make([]token.Token, len(e.out), capacity)
	copy(next, e.out)
	e.out = next
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
	e.expandInvocation(f, inv, body, m.Name)
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
	if len(args) == 0 && m.ParamCount > 0 {
		args = [][]ptok{nil}
	}
	if len(args) > m.ParamCount && m.ParamCount > 0 {
		last := m.ParamCount - 1
		merged := append([]ptok(nil), args[last]...)
		comma := ptok{Token: token.Token{Kind: token.Comma, Start: closeParen.Start, End: closeParen.Start}, text: ",", file: f.fileIndex}
		for _, extra := range args[m.ParamCount:] {
			merged = append(merged, comma)
			merged = append(merged, extra...)
		}
		args = append(args[:last], merged)
	}
	if len(args) < m.ParamCount && !m.FlexiblePattern && f.currentActive() {
		e.diag(f, CodeMacroArgumentMismatch, diagnostic.SeverityWarning,
			"macro invocation argument count mismatch", spanOf(tok, closeParen))
	}
	inv := invocationSpan(f.fileIndex, tok, closeParen)
	body := substituteParams(m, args, inv)
	e.expandInvocation(f, inv, body, m.Name)
}

func (e *engine) recordInvocation(inv token.Span) {
	e.invocations = append(e.invocations, MacroInvocation{
		File: inv.File,
		Range: ByteRange{
			Start: inv.Start.Offset,
			End:   inv.End.Offset,
		},
	})
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
		if e.pollCancellation() {
			return nil, token.Token{}, false
		}
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
		item := toPtok(f.source, t, f.fileIndex)
		retainTokenOrigin(&item)
		current = append(current, item)
		f.advance()
	}
}

func substituteParams(m Macro, args [][]ptok, inv token.Span) []ptok {
	if m.textualBody {
		return substituteText(m, args, inv)
	}
	var out []ptok
	for _, bt := range m.Body {
		if bt.Kind == token.MacroParam {
			if idx, isParam := parseParamIndex(bt.text); isParam {
				slot, declared := m.ParamSlots[idx]
				if declared && slot < len(args) {
					for _, at := range args[slot] {
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

func (e *engine) expandInvocation(f *frame, inv token.Span, toks []ptok, name string) {
	index := len(e.invocations)
	e.recordInvocation(inv)
	start := len(e.expandedBuf)
	sourcePos := f.pos
	e.expandReplacement(f, toks, &expansionCursor{frame: f}, hideSet{}.with(name), 1)
	if f.pos > sourcePos {
		e.invocations[index].Range.End = f.toks[f.pos-1].End.Offset
	}
	if end := len(e.expandedBuf); end > start {
		if start > 0 {
			start++ // The first emitted token has a separating space.
		}
		e.invocations[index].Expansion = ByteRange{Start: start, End: end}
	}
}

// A replacement may end in the prefix of another macro. Its lookahead can
// continue into the enclosing replacement and then the unconsumed source.
// A failed match does not advance any of those cursors.
type expansionCursor struct {
	tokens []ptok
	pos    int
	tail   *expansionCursor
	frame  *frame
}

func (c *expansionCursor) peek(offset int, pattern bool) (ptok, bool) {
	for c.frame == nil {
		index := c.pos + offset
		if index < len(c.tokens) {
			if pattern && index > 0 && endsLine(c.tokens[index-1].Token) {
				return ptok{}, false
			}
			return c.tokens[index], true
		}
		offset -= len(c.tokens) - c.pos
		c = c.tail
	}
	f := c.frame
	index := f.pos + offset
	if index >= len(f.toks) || endsLine(f.toks[index-1]) {
		return ptok{}, false
	}
	return toPtok(f.source, f.toks[index], f.fileIndex), true
}

func (c *expansionCursor) consume(count int) {
	for c.frame == nil {
		available := len(c.tokens) - c.pos
		if count <= available {
			c.pos += count
			return
		}
		c.pos += available
		count -= available
		c = c.tail
	}
	for range count {
		c.frame.advance()
	}
}

func (e *engine) consumeNested(f *frame, cursor *expansionCursor, start, end ptok, count int) token.Span {
	inv := nestedInvocationSpan(start, end)
	before := f.pos
	cursor.consume(count)
	if f.pos > before {
		inv.End = f.toks[f.pos-1].End
		e.recordInvocation(inv)
	}
	return inv
}

func (e *engine) expandReplacement(f *frame, toks []ptok, tail *expansionCursor, hide hideSet, depth int) {
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
		e.appendAllOut(toks)
		return
	}
	cursor := &expansionCursor{tokens: toks, tail: tail}
	for cursor.pos < len(toks) {
		if e.truncated || e.pollCancellation() {
			break
		}
		t := toks[cursor.pos]
		cursor.pos++
		if e.condition != nil && e.condition.keepUnexpanded(t.Kind) {
			e.appendOut(t)
			continue
		}
		if t.Kind == token.Identifier && !hide[t.text] {
			if m, ok := e.macros.lookup(t.text); ok {
				if m.pattern != nil {
					args, consumed, matched := matchPattern(m, func(index int) (ptok, bool) {
						return cursor.peek(index, true)
					}, e.pollCancellation)
					if matched {
						last, _ := cursor.peek(consumed-1, true)
						inv := e.consumeNested(f, cursor, t, last, consumed)
						e.expandReplacement(f, substituteParams(m, args, inv), cursor, hide.with(m.Name), depth+1)
						continue
					}
					e.appendOut(t)
					continue
				}
				switch m.Kind {
				case MacroObjectLike:
					inv := nestedInvocationSpan(t, t)
					body := make([]ptok, len(m.Body))
					for j, bt := range m.Body {
						body[j] = wrapOrigin(bt, inv, m.Name)
					}
					e.expandReplacement(f, body, cursor, hide.with(m.Name), depth+1)
					continue
				case MacroFunctionLike:
					if next, ok := cursor.peek(0, false); ok && next.Kind == token.LParen {
						args, consumed, ok := e.collectReplacementArgs(cursor)
						if ok {
							last, _ := cursor.peek(consumed-1, false)
							inv := e.consumeNested(f, cursor, t, last, consumed)
							sub := substituteParams(m, args, inv)
							e.expandReplacement(f, sub, cursor, hide.with(m.Name), depth+1)
							continue
						}
					}
				}
			}
		}
		e.appendOut(t)
	}
}

// collectReplacementArgs starts after the opening parenthesis at offset zero.
func (e *engine) collectReplacementArgs(cursor *expansionCursor) (args [][]ptok, next int, ok bool) {
	if first, ok := cursor.peek(1, false); ok && first.Kind == token.RParen {
		return nil, 2, true
	}
	depth := 0
	var current []ptok
	for i := 1; !e.pollCancellation(); i++ {
		t, ok := cursor.peek(i, false)
		if !ok || t.Kind == token.EOF {
			return nil, 0, false
		}
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
				continue
			}
		}
		current = append(current, t)
	}
	return nil, 0, false
}
