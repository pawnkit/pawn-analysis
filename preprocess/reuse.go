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
