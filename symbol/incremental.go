package symbol

import (
	"bytes"

	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawn-parser/token"
	"github.com/pawnkit/pawnkit-core/source"
)

// PatchReference returns a table with one reference name updated.
func PatchReference(previous *Table, span source.Span, name string) (*Table, bool) {
	if previous == nil || name == "" {
		return nil, false
	}
	index := -1
	for i := range previous.References {
		if previous.References[i].Span == span {
			if index != -1 {
				return nil, false
			}
			index = i
		}
	}
	if index == -1 {
		return nil, false
	}

	next := &Table{
		File: previous.File, Symbols: previous.Symbols, Scopes: previous.Scopes,
		References:  append([]Reference(nil), previous.References...),
		Diagnostics: previous.Diagnostics, declarations: previous.declarations,
	}
	reference := &next.References[index]
	if reference.Resolved == 0 {
		return nil, false
	}
	reference.Name = name
	reference.Resolved = 0
	if resolved, ok := next.Lookup(reference.Scope, name); ok {
		reference.Resolved = resolved.ID
	}
	if reference.Resolved == 0 {
		return nil, false
	}
	next.references = make(map[source.Span]ID, len(previous.references))
	for key, value := range previous.references {
		next.references[key] = value
	}
	if reference.Resolved == 0 {
		delete(next.references, span)
	} else {
		next.references[span] = reference.Resolved
	}
	return next, true
}

// RebaseParenthesized returns a table remapped after wrapping or unwrapping text.
func RebaseParenthesized(
	previous *Table,
	previousSource, currentSource []byte,
	previousTokens, tokens []token.Token,
	before, after parser.ByteRange,
) (*Table, bool) {
	if previous == nil || len(previous.Diagnostics) != 0 ||
		!parenthesesOnly(previousSource, currentSource, before, after) {
		return nil, false
	}
	spans, ok := identifierSpanMap(previous.File, previousSource, currentSource, previousTokens, tokens)
	if !ok {
		return nil, false
	}
	next := &Table{
		File: previous.File, Scopes: previous.Scopes,
		Symbols:     append([]Symbol(nil), previous.Symbols...),
		References:  append([]Reference(nil), previous.References...),
		Diagnostics: previous.Diagnostics,
	}
	next.declarations = make(map[source.Span]ID, len(previous.declarations))
	for index := range next.Symbols {
		span, found := spans[next.Symbols[index].Span]
		if !found {
			return nil, false
		}
		next.Symbols[index].Span = span
		next.declarations[span] = next.Symbols[index].ID
	}
	next.references = make(map[source.Span]ID, len(previous.references))
	for index := range next.References {
		span, found := spans[next.References[index].Span]
		if !found {
			return nil, false
		}
		next.References[index].Span = span
		if next.References[index].Resolved != 0 {
			next.references[span] = next.References[index].Resolved
		}
	}
	return next, true
}

func parenthesesOnly(previous, current []byte, before, after parser.ByteRange) bool {
	if before.Start < 0 || before.End < before.Start || before.End > len(previous) ||
		after.Start < 0 || after.End < after.Start || after.End > len(current) {
		return false
	}
	old, next := previous[before.Start:before.End], current[after.Start:after.End]
	return len(old) != 0 && len(next) == len(old)+2 && next[0] == '(' && next[len(next)-1] == ')' &&
		bytes.Equal(next[1:len(next)-1], old) ||
		len(next) != 0 && len(old) == len(next)+2 && old[0] == '(' && old[len(old)-1] == ')' &&
			bytes.Equal(old[1:len(old)-1], next)
}

func identifierSpanMap(
	file source.FileID,
	previous, current []byte,
	previousTokens, currentTokens []token.Token,
) (map[source.Span]source.Span, bool) {
	oldIdentifiers := identifierTokens(previousTokens)
	newIdentifiers := identifierTokens(currentTokens)
	if len(oldIdentifiers) != len(newIdentifiers) {
		return nil, false
	}
	spans := make(map[source.Span]source.Span, len(oldIdentifiers))
	for index, old := range oldIdentifiers {
		next := newIdentifiers[index]
		if old.Text(previous) != next.Text(current) {
			return nil, false
		}
		oldSpan := source.Span{
			File: file, Start: source.Offset(old.Start.Offset), End: source.Offset(old.End.Offset),
		}
		spans[oldSpan] = source.Span{
			File: file, Start: source.Offset(next.Start.Offset), End: source.Offset(next.End.Offset),
		}
	}
	return spans, true
}

func identifierTokens(tokens []token.Token) []token.Token {
	result := make([]token.Token, 0, len(tokens)/4)
	for _, item := range tokens {
		if item.Kind == token.Identifier {
			result = append(result, item)
		}
	}
	return result
}
