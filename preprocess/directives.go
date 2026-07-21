package preprocess

import (
	"strings"

	"github.com/pawnkit/pawn-parser/lexer"
	"github.com/pawnkit/pawn-parser/token"
	"github.com/pawnkit/pawnkit-core/diagnostic"
)

func tokenizeBody(text string) []ptok {
	src := []byte(text)
	toks := lexer.Tokenize(src)
	if n := len(toks); n > 0 && toks[n-1].Kind == token.EOF {
		toks = toks[:n-1]
	}
	return toPtoks(src, toks)
}

func spanOf(start, end token.Token) ByteRange {
	return ByteRange{Start: start.Start.Offset, End: end.End.Offset}
}

func (e *engine) diag(f *frame, code Code, severity diagnostic.Severity, message string, r ByteRange) {
	e.diags = append(e.diags, Diagnostic{
		File: f.fileIndex, Code: code, Severity: severity, Message: message, Range: r,
	})
}

func (e *engine) collectRestOfLine(f *frame) []token.Token {
	var out []token.Token
	for !f.atEnd() {
		t := f.advance()
		out = append(out, t)
		if endsLine(t) {
			break
		}
	}
	return out
}

func (e *engine) handleDirectiveLine(f *frame) {
	hash := f.advance()
	if f.atEnd() {
		return
	}
	kwTok := f.cur()
	name := kwTok.Text(f.source)
	kw := classifyDirective(name)
	lineEndedAtKeyword := false
	if kwTok.Kind != token.Hash {
		f.advance()
		lineEndedAtKeyword = endsLine(kwTok)
	}

	switch kw {
	case dirDefine:
		e.handleDefine(f, hash)
	case dirUndef:
		e.handleUndef(f, hash, lineEndedAtKeyword)
	case dirIf:
		e.handleIf(f, hash, lineEndedAtKeyword)
	case dirElseif:
		e.handleElseif(f, hash, lineEndedAtKeyword)
	case dirElse:
		e.handleElse(f, hash, lineEndedAtKeyword)
	case dirEndif:
		e.handleEndif(f, hash, lineEndedAtKeyword)
	case dirInclude:
		e.handleInclude(f, hash, false)
	case dirTryInclude:
		e.handleInclude(f, hash, true)
	case dirError:
		e.handleUserMessage(f, hash, kwTok.End.Offset, true, lineEndedAtKeyword)
	case dirWarning:
		e.handleUserMessage(f, hash, kwTok.End.Offset, false, lineEndedAtKeyword)
	case dirAssert:
		e.handleAssert(f, hash, lineEndedAtKeyword)
	case dirEndinput:
		e.handleEndinput(f, lineEndedAtKeyword)
	case dirPragma, dirLine, dirFile:
		if !lineEndedAtKeyword {
			e.collectRestOfLine(f)
		}
	default:
		if f.currentActive() {
			e.diag(f, CodeUnknownDirective, diagnostic.SeverityWarning,
				"unknown preprocessor directive #"+name, spanOf(hash, kwTok))
		}
		if !lineEndedAtKeyword {
			e.collectRestOfLine(f)
		}
	}
}

func (e *engine) handleDefine(f *frame, hash token.Token) {
	if !f.at(token.Identifier) {
		if !endsLine(f.toks[f.pos-1]) {
			e.collectRestOfLine(f)
		}
		if f.currentActive() {
			e.diag(f, CodeMalformedDefine, diagnostic.SeverityError, "#define missing macro name", spanOf(hash, hash))
		}
		return
	}
	nameTok := f.advance()
	name := nameTok.Text(f.source)

	m := Macro{Name: name, Kind: MacroObjectLike, File: f.fileIndex, DefSpan: spanOf(hash, nameTok)}

	if f.cur().Kind == token.LParen && nameTok.End.Offset == f.cur().Start.Offset {
		lparen := f.advance()
		m.Kind = MacroFunctionLike
		m.ParamSlots = make(map[int]int)
		m.NamedParams = make(map[string]int)
		idx := 0
		for !f.atEnd() {
			if f.cur().Kind == token.RParen {
				f.advance()
				break
			}
			pt := f.cur()
			switch pt.Kind {
			case token.MacroParam:
				f.advance()
				if n, ok := parseParamIndex(pt.Text(f.source)); ok {
					m.ParamSlots[n] = idx
				}
				idx++
			case token.Identifier:
				f.advance()
				m.NamedParams[pt.Text(f.source)] = idx
				idx++
			default:
				f.advance()
			}
			if f.cur().Kind == token.Comma {
				f.advance()
				continue
			}
			if f.cur().Kind == token.RParen {
				f.advance()
				break
			}
			if endsLine(pt) || f.atEnd() {
				break
			}
		}
		m.ParamCount = idx
		_ = lparen
	}

	if !endsLine(f.toks[f.pos-1]) {
		m.Body = toPtoks(f.source, e.collectRestOfLine(f), f.fileIndex)
	}

	if !f.currentActive() {
		return
	}
	previous, redefined := e.macros.define(m)
	if redefined && !macroEqual(previous, m) {
		e.diag(f, CodeMalformedDefine, diagnostic.SeverityWarning,
			"macro '"+name+"' redefined without #undef", spanOf(hash, nameTok))
	}
}

func macroEqual(a, b Macro) bool {
	if a.Kind != b.Kind || a.ParamCount != b.ParamCount || len(a.ParamSlots) != len(b.ParamSlots) || len(a.NamedParams) != len(b.NamedParams) || len(a.Body) != len(b.Body) {
		return false
	}
	for label, slot := range a.ParamSlots {
		if other, ok := b.ParamSlots[label]; !ok || other != slot {
			return false
		}
	}
	for name, slot := range a.NamedParams {
		if other, ok := b.NamedParams[name]; !ok || other != slot {
			return false
		}
	}
	for i := range a.Body {
		if a.Body[i].Kind != b.Body[i].Kind || a.Body[i].text != b.Body[i].text {
			return false
		}
	}
	return true
}

func (e *engine) handleUndef(f *frame, hash token.Token, lineEnded bool) {
	if lineEnded || !f.at(token.Identifier) {
		if !lineEnded {
			e.collectRestOfLine(f)
		}
		return
	}
	nameTok := f.advance()
	if !endsLine(nameTok) {
		e.collectRestOfLine(f)
	}
	if f.currentActive() {
		e.macros.undef(nameTok.Text(f.source))
	}
	_ = hash
}

func (e *engine) handleIf(f *frame, hash token.Token, lineEnded bool) {
	var rest []token.Token
	if !lineEnded {
		rest = e.collectRestOfLine(f)
	}
	parentActive := f.currentActive()

	if len(f.condStack) >= e.opts.MaxConditionalDepth {
		e.truncated = true
		if !e.depthLimitWarned {
			e.depthLimitWarned = true
			e.diag(f, CodeConditionalDepthLimit, diagnostic.SeverityError,
				"maximum conditional nesting depth exceeded", spanOf(hash, hash))
		}
		f.condStack = append(f.condStack, condFrame{parentActive: parentActive, branchActive: false, taken: true, overflow: true})
		return
	}

	branchActive, evaluated := e.evaluateBranch(f, hash, rest, parentActive)
	f.condStack = append(f.condStack, condFrame{parentActive: parentActive, branchActive: branchActive, taken: branchActive})
	e.openBranch(f, DirectiveIf, hash, rest, branchActive, evaluated)
}

func (e *engine) evaluateBranch(f *frame, hash token.Token, rest []token.Token, parentActive bool) (active, evaluated bool) {
	if !parentActive {
		return false, false
	}
	v, ok := evalCondition(rest, f.source, e.macros)
	if !ok {
		r := spanOf(hash, hash)
		if len(rest) > 0 {
			r = spanOf(rest[0], rest[len(rest)-1])
		}
		e.diag(f, CodeUnresolvableCondition, diagnostic.SeverityWarning,
			"could not evaluate #if/#elseif condition; treating branch as inactive", r)
		return false, true
	}
	return v != 0, true
}

func (e *engine) handleElseif(f *frame, hash token.Token, lineEnded bool) {
	var rest []token.Token
	if !lineEnded {
		rest = e.collectRestOfLine(f)
	}
	if len(f.condStack) == 0 {
		if f.currentActive() {
			e.diag(f, CodeUnmatchedElseif, diagnostic.SeverityError, "#elseif without matching #if", spanOf(hash, hash))
		}
		return
	}
	top := &f.condStack[len(f.condStack)-1]
	e.closeBranch(f, hash)
	if top.overflow {
		return
	}
	if top.taken {
		top.branchActive = false
		e.openBranch(f, DirectiveElseif, hash, rest, false, false)
		return
	}
	active, evaluated := e.evaluateBranch(f, hash, rest, top.parentActive)
	top.branchActive = active
	if active {
		top.taken = true
	}
	e.openBranch(f, DirectiveElseif, hash, rest, active, evaluated)
}

func (e *engine) handleElse(f *frame, hash token.Token, lineEnded bool) {
	if !lineEnded {
		e.collectRestOfLine(f)
	}
	if len(f.condStack) == 0 {
		if f.currentActive() {
			e.diag(f, CodeUnmatchedElse, diagnostic.SeverityError, "#else without matching #if", spanOf(hash, hash))
		}
		return
	}
	top := &f.condStack[len(f.condStack)-1]
	e.closeBranch(f, hash)
	if top.overflow {
		return
	}
	active := top.parentActive && !top.taken
	top.branchActive = active
	if active {
		top.taken = true
	}
	e.openBranch(f, DirectiveElse, hash, nil, active, top.parentActive)
}

func (e *engine) handleEndif(f *frame, hash token.Token, lineEnded bool) {
	if !lineEnded {
		e.collectRestOfLine(f)
	}
	if len(f.condStack) == 0 {
		e.diag(f, CodeUnmatchedEndif, diagnostic.SeverityError, "#endif without matching #if", spanOf(hash, hash))
		return
	}
	top := f.condStack[len(f.condStack)-1]
	if !top.overflow {
		e.closeBranch(f, hash)
	}
	f.condStack = f.condStack[:len(f.condStack)-1]
}

func (e *engine) openBranch(f *frame, kind DirectiveKind, hash token.Token, condition []token.Token, active, evaluated bool) {
	b := Branch{
		File: f.fileIndex, Directive: kind, Depth: len(f.condStack),
		DirectiveSpan: ByteRange{Start: hash.Start.Offset, End: f.toks[f.pos-1].End.Offset},
		Active:        active, Evaluated: evaluated,
	}
	if len(condition) > 0 {
		b.ConditionSpan = spanOf(condition[0], condition[len(condition)-1])
	}
	b.BodySpan = ByteRange{Start: b.DirectiveSpan.End, End: b.DirectiveSpan.End}
	e.branches = append(e.branches, b)
	idx := len(e.branches) - 1
	if len(f.condStack) > 0 {
		f.condStack[len(f.condStack)-1].openBranchIndex = idx
	}
}

func (e *engine) closeBranch(f *frame, hash token.Token) {
	if len(f.condStack) == 0 {
		return
	}
	idx := f.condStack[len(f.condStack)-1].openBranchIndex
	if idx < 0 || idx >= len(e.branches) {
		return
	}
	e.branches[idx].BodySpan.End = hash.Start.Offset
}

func (e *engine) handleUserMessage(f *frame, hash token.Token, msgStart int, isError bool, lineEnded bool) {
	end := msgStart
	if !lineEnded {
		e.collectRestOfLine(f)
		end = f.toks[f.pos-1].End.Offset
	}
	if !f.currentActive() {
		return
	}
	msg := strings.TrimSpace(string(sliceSource(f.source, msgStart, end)))
	code, severity := CodeUserWarning, diagnostic.SeverityWarning
	if isError {
		code, severity = CodeUserError, diagnostic.SeverityError
	}
	if msg == "" {
		msg = "#error"
		if !isError {
			msg = "#warning"
		}
	}
	e.diag(f, code, severity, msg, ByteRange{Start: hash.Start.Offset, End: end})
}

func sliceSource(source []byte, start, end int) []byte {
	if start < 0 {
		start = 0
	}
	if end > len(source) {
		end = len(source)
	}
	if end < start {
		return nil
	}
	return source[start:end]
}

func (e *engine) handleAssert(f *frame, hash token.Token, lineEnded bool) {
	var rest []token.Token
	if !lineEnded {
		rest = e.collectRestOfLine(f)
	}
	if !f.currentActive() {
		return
	}
	r := spanOf(hash, hash)
	if len(rest) > 0 {
		r = spanOf(rest[0], rest[len(rest)-1])
	}
	v, ok := evalCondition(rest, f.source, e.macros)
	switch {
	case !ok:
		e.diag(f, CodeAssertUnknown, diagnostic.SeverityHint, "could not evaluate #assert condition", r)
	case v == 0:
		e.diag(f, CodeAssertFailed, diagnostic.SeverityError, "#assert failed", r)
	}
}

func (e *engine) handleEndinput(f *frame, lineEnded bool) {
	if !lineEnded {
		e.collectRestOfLine(f)
	}
	if f.currentActive() {
		f.endinput = true
	}
}
