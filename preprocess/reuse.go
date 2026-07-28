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

// ReuseCompatibleContext retains the dependency graph for a same-width token
// edit. Expanded source still contains the previous token spelling.
func ReuseCompatibleContext(
	ctx context.Context,
	src []byte,
	uri string,
	cache *TokenCache,
	previous *Result,
) (*Result, []ByteRange, bool, error) {
	if previous == nil || len(src) != len(previous.Source) {
		return nil, nil, false, nil
	}
	tokens, err := cache.tokenizeContext(ctx, true, uri, src)
	if err != nil {
		return nil, nil, false, err
	}
	if len(tokens) != len(previous.OriginalTokens) {
		return nil, nil, false, nil
	}
	changed := make([]ByteRange, 0, 1)
	for i := range tokens {
		current, before := tokens[i], previous.OriginalTokens[i]
		if current.Kind != before.Kind ||
			current.Start != before.Start ||
			current.End != before.End ||
			!sameTrivia(current.LeadingTrivia, before.LeadingTrivia) ||
			!sameTrivia(current.TrailingTrivia, before.TrailingTrivia) {
			return nil, nil, false, nil
		}
		if current.Text(src) == before.Text(previous.Source) {
			continue
		}
		if directiveToken(tokens, i) {
			return nil, nil, false, nil
		}
		if _, macro := previous.Macros[before.Text(previous.Source)]; macro {
			return nil, nil, false, nil
		}
		if _, macro := previous.Macros[current.Text(src)]; macro {
			return nil, nil, false, nil
		}
		changed = append(changed, ByteRange{Start: current.Start.Offset, End: current.End.Offset})
	}
	if len(changed) == 0 {
		return nil, nil, false, nil
	}

	result := *previous
	result.Source = src
	result.OriginalTokens = tokens
	result.Files = append([]FileInfo(nil), previous.Files...)
	if len(result.Files) != 0 {
		result.Files[0].Content = src
	}
	return &result, changed, true, nil
}

func directiveToken(tokens []token.Token, position int) bool {
	for position >= 0 {
		current := tokens[position]
		if current.Kind == token.Hash {
			return true
		}
		for _, trivia := range current.LeadingTrivia {
			if trivia.Kind == token.Newline {
				return false
			}
		}
		if position > 0 {
			for _, trivia := range tokens[position-1].TrailingTrivia {
				if trivia.Kind == token.Newline {
					return false
				}
			}
		}
		position--
	}
	return false
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
