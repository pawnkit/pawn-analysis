package preprocess

import (
	"context"

	"github.com/pawnkit/pawn-parser/token"
)

// ReuseTriviaContext reuses preprocessing when only trivia text changed.
func ReuseTriviaContext(
	ctx context.Context,
	src []byte,
	uri string,
	cache *TokenCache,
	previous *Result,
) (*Result, bool, error) {
	if previous == nil {
		return nil, false, nil
	}
	tokens, err := cache.tokenizeContext(ctx, true, uri, src)
	if err != nil {
		return nil, false, err
	}
	if !sameLexicalLayout(src, tokens, previous.Source, previous.OriginalTokens) {
		return nil, false, nil
	}

	result := *previous
	result.Source = src
	result.OriginalTokens = tokens
	result.Files = append([]FileInfo(nil), previous.Files...)
	if len(result.Files) != 0 {
		result.Files[0].Content = src
	}
	return &result, true, nil
}

// CompatibleEdit maps one changed range between source revisions.
type CompatibleEdit struct {
	Before ByteRange
	After  ByteRange
}

// ReuseCompatibleContext retains the dependency graph for a local token edit.
// Expanded source still contains the previous local body.
func ReuseCompatibleContext(
	ctx context.Context,
	src []byte,
	uri string,
	cache *TokenCache,
	previous *Result,
) (*Result, CompatibleEdit, bool, error) {
	if previous == nil {
		return nil, CompatibleEdit{}, false, nil
	}
	edit, changed := compatibleEdit(previous.Source, src)
	if !changed || directiveRange(previous.Source, edit.Before) || directiveRange(src, edit.After) {
		return nil, CompatibleEdit{}, false, nil
	}
	tokens, err := cache.tokenizeContext(ctx, true, uri, src)
	if err != nil {
		return nil, CompatibleEdit{}, false, err
	}
	if touchesMacro(tokens, src, edit.After, previous.Macros) ||
		touchesMacro(previous.OriginalTokens, previous.Source, edit.Before, previous.Macros) {
		return nil, CompatibleEdit{}, false, nil
	}

	result := *previous
	result.Source = src
	result.OriginalTokens = tokens
	result.Files = append([]FileInfo(nil), previous.Files...)
	if len(result.Files) != 0 {
		result.Files[0].Content = src
	}
	result.Branches = append([]Branch(nil), previous.Branches...)
	for i := range result.Branches {
		if result.Branches[i].File != 0 {
			continue
		}
		result.Branches[i].DirectiveSpan = shiftRange(result.Branches[i].DirectiveSpan, edit)
		result.Branches[i].ConditionSpan = shiftRange(result.Branches[i].ConditionSpan, edit)
		result.Branches[i].BodySpan = shiftRange(result.Branches[i].BodySpan, edit)
	}
	result.Includes = append([]Include(nil), previous.Includes...)
	for i := range result.Includes {
		if result.Includes[i].File == 0 {
			result.Includes[i].DirectiveSpan = shiftRange(result.Includes[i].DirectiveSpan, edit)
		}
	}
	result.MacroInvocations = append([]MacroInvocation(nil), previous.MacroInvocations...)
	for i := range result.MacroInvocations {
		if result.MacroInvocations[i].File == 0 {
			result.MacroInvocations[i].Range = shiftRange(result.MacroInvocations[i].Range, edit)
		}
	}
	result.Diagnostics = append([]Diagnostic(nil), previous.Diagnostics...)
	for i := range result.Diagnostics {
		if result.Diagnostics[i].File == 0 {
			result.Diagnostics[i].Range = shiftRange(result.Diagnostics[i].Range, edit)
		}
	}
	result.Macros = make(map[string]Macro, len(previous.Macros))
	for name, macro := range previous.Macros {
		if macro.File == 0 {
			macro.DefSpan = shiftRange(macro.DefSpan, edit)
		}
		result.Macros[name] = macro
	}
	return &result, edit, true, nil
}

func compatibleEdit(before, after []byte) (CompatibleEdit, bool) {
	start := 0
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	beforeEnd, afterEnd := len(before), len(after)
	for beforeEnd > start && afterEnd > start && before[beforeEnd-1] == after[afterEnd-1] {
		beforeEnd--
		afterEnd--
	}
	return CompatibleEdit{
		Before: ByteRange{Start: start, End: beforeEnd},
		After:  ByteRange{Start: start, End: afterEnd},
	}, start != beforeEnd || start != afterEnd
}

func directiveRange(src []byte, changed ByteRange) bool {
	start := changed.Start
	if start == len(src) && start > 0 {
		start--
	}
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	end := changed.End
	for end < len(src) && src[end] != '\n' {
		end++
	}
	for line := start; line <= end && line < len(src); {
		lineEnd := line
		for lineEnd < len(src) && src[lineEnd] != '\n' {
			lineEnd++
		}
		position := line
		for position < lineEnd && (src[position] == ' ' || src[position] == '\t') {
			position++
		}
		if position < lineEnd && src[position] == '#' {
			return true
		}
		line = lineEnd + 1
	}
	return false
}

func touchesMacro(tokens []token.Token, src []byte, changed ByteRange, macros map[string]Macro) bool {
	for _, current := range tokens {
		if current.End.Offset < changed.Start || current.Start.Offset > changed.End {
			continue
		}
		if _, macro := macros[current.Text(src)]; macro {
			return true
		}
	}
	return false
}

func shiftRange(current ByteRange, edit CompatibleEdit) ByteRange {
	return ByteRange{
		Start: shiftOffset(current.Start, edit),
		End:   shiftOffset(current.End, edit),
	}
}

func shiftOffset(offset int, edit CompatibleEdit) int {
	if offset <= edit.Before.Start {
		return offset
	}
	if offset >= edit.Before.End {
		return offset + edit.After.End - edit.Before.End
	}
	if offset-edit.Before.Start > edit.After.End-edit.After.Start {
		return edit.After.End
	}
	return edit.After.Start + offset - edit.Before.Start
}

func sameLexicalLayout(leftSource []byte, left []token.Token, rightSource []byte, right []token.Token) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Kind != right[i].Kind ||
			left[i].Start != right[i].Start ||
			left[i].End != right[i].End ||
			left[i].Text(leftSource) != right[i].Text(rightSource) ||
			!sameTrivia(left[i].LeadingTrivia, right[i].LeadingTrivia) ||
			!sameTrivia(left[i].TrailingTrivia, right[i].TrailingTrivia) {
			return false
		}
	}
	return true
}

func sameTrivia(left, right []token.Trivia) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
