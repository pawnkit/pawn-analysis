package preprocess

import (
	"strings"

	"github.com/pawnkit/pawn-parser/lexer"
	"github.com/pawnkit/pawn-parser/token"
)

// matchPattern works on token accessors so scanning an invocation never copies
// the remaining file. Captures stop at their next literal delimiter, with nested
// groups and quoted tokens kept intact. Failed matches consume no source.
func matchPattern(m Macro, at func(int) (ptok, bool), cancelled func() bool) ([][]ptok, int, bool) {
	args := make([][]ptok, m.ParamCount)
	position := 0
	for index := 0; index < len(m.pattern.tokens); index++ {
		if cancelled() {
			return nil, 0, false
		}
		pattern := m.pattern.tokens[index]
		if label, capture := parseParamIndex(pattern.text); pattern.Kind == token.MacroParam && capture {
			if index+1 == len(m.pattern.tokens) {
				return nil, 0, false
			}
			delimiter := m.pattern.tokens[index+1]
			var captured []ptok
			var stack []token.Kind
			for {
				if cancelled() {
					return nil, 0, false
				}
				item, ok := at(position)
				if !ok || item.Kind == token.EOF {
					return nil, 0, false
				}
				if len(stack) == 0 && samePatternToken(item, delimiter) {
					break
				}
				switch item.Kind {
				case token.LParen:
					stack = append(stack, token.RParen)
				case token.LBracket:
					stack = append(stack, token.RBracket)
				case token.LBrace:
					stack = append(stack, token.RBrace)
				case token.RParen, token.RBracket, token.RBrace:
					if len(stack) == 0 || stack[len(stack)-1] != item.Kind {
						return nil, 0, false
					}
					stack = stack[:len(stack)-1]
				}
				captured = append(captured, item)
				position++
			}
			args[m.ParamSlots[label]] = captured
			continue
		}
		item, ok := at(position)
		if !ok || !samePatternToken(item, pattern) {
			return nil, 0, false
		}
		position++
	}
	return args, position, true
}

func samePatternToken(a, b ptok) bool {
	return a.Kind == b.Kind && a.text == b.text
}

func (e *engine) expandPatternAt(f *frame, name token.Token, m Macro) bool {
	args, consumed, ok := matchPattern(m, func(index int) (ptok, bool) {
		at := f.pos + index
		if at >= len(f.toks) || endsLine(f.toks[at-1]) {
			return ptok{}, false
		}
		return toPtok(f.source, f.toks[at], f.fileIndex), true
	}, e.pollCancellation)
	if !ok {
		return false
	}
	last := name
	for range consumed {
		last = f.advance()
	}
	inv := invocationSpan(f.fileIndex, name, last)
	e.expandInvocation(f, inv, substituteParams(m, args, inv), m.Name)
	return true
}

// Most replacements can reuse tokens. Re-lex when a capture touches text that
// can form a different token, e.g. prefix_%0, %0_suffix, %0.0 or +%0. Brackets
// around ordinary expressions do not require another lexer pass.
func needsTextSubstitution(m Macro) bool {
	body := m.Body
	for index, item := range body {
		_, named := m.NamedParams[item.text]
		if item.Kind != token.MacroParam && !named {
			continue
		}
		if index > 0 && body[index-1].End.Offset == item.Start.Offset && joiningBoundary(body[index-1].text, item.text) {
			return true
		}
		if index+1 < len(body) && item.End.Offset == body[index+1].Start.Offset && joiningBoundary(item.text, body[index+1].text) {
			return true
		}
	}
	return false
}

func joiningBoundary(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	a, b := left[len(left)-1], right[0]
	return textBoundary(a) && textBoundary(b)
}

func textBoundary(char byte) bool {
	return char == '_' || char == '@' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9' || strings.ContainsRune(".%+-*/<>=!&|^~:#\"'", rune(char))
}

type substitutionSegment struct {
	end    int
	parent *token.Origin
}

// Pawn replacements are textual, unlike C token substitution. Preserve source
// adjacency, substitute captures, then lex the resulting spelling once. Segment
// origins retain both the invocation and the contributing definition/argument.
func substituteText(m Macro, args [][]ptok, inv token.Span) []ptok {
	var spelling strings.Builder
	var segments []substitutionSegment
	appendItem := func(item ptok) {
		spelling.WriteString(item.text)
		retainTokenOrigin(&item)
		segments = append(segments, substitutionSegment{end: spelling.Len(), parent: item.Origin})
	}
	for index, item := range m.Body {
		if index > 0 && m.Body[index-1].End.Offset != item.Start.Offset {
			spelling.WriteByte(' ')
		}
		slot, capture := m.NamedParams[item.text]
		if item.Kind == token.MacroParam {
			if label, ok := parseParamIndex(item.text); ok {
				slot, capture = m.ParamSlots[label]
			}
		}
		if capture {
			if slot < len(args) {
				for at, argument := range args[slot] {
					if at > 0 && args[slot][at-1].End.Offset != argument.Start.Offset {
						spelling.WriteByte(' ')
					}
					appendItem(argument)
				}
			}
		} else {
			appendItem(item)
		}
	}
	source := []byte(spelling.String())
	tokens := lexer.Tokenize(source)
	if len(tokens) > 0 && tokens[len(tokens)-1].Kind == token.EOF {
		tokens = tokens[:len(tokens)-1]
	}
	items := toPtoks(source, tokens, inv.File)
	segment := 0
	for index := range items {
		for segment < len(segments) && segments[segment].end <= items[index].Start.Offset {
			segment++
		}
		var parent *token.Origin
		if segment < len(segments) {
			parent = segments[segment].parent
		}
		items[index].Origin = &token.Origin{Span: inv, Macro: m.Name, Parent: parent}
	}
	return items
}
