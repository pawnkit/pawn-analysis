package preprocess

import (
	"context"
	"crypto/sha256"
	"sync"

	"github.com/pawnkit/pawn-parser/lexer"
	"github.com/pawnkit/pawn-parser/token"
)

type TokenCache struct {
	mu      sync.RWMutex
	entries map[string]tokenCacheEntry
}

type tokenCacheEntry struct {
	hash   [sha256.Size]byte
	tokens []token.Token
}

func NewTokenCache() *TokenCache {
	return &TokenCache{entries: make(map[string]tokenCacheEntry)}
}

func (c *TokenCache) tokenize(uri string, content []byte) []token.Token {
	tokens, _ := c.tokenizeContext(context.Background(), false, uri, content)
	return tokens
}

func (c *TokenCache) tokenizeContext(
	ctx context.Context,
	cancellable bool,
	uri string,
	content []byte,
) ([]token.Token, error) {
	if c == nil {
		if cancellable {
			return lexer.TokenizeContext(ctx, content)
		}
		return lexer.Tokenize(content), nil
	}
	hash := sha256.Sum256(content)
	c.mu.RLock()
	entry, ok := c.entries[uri]
	c.mu.RUnlock()
	if ok && entry.hash == hash {
		return entry.tokens, nil
	}
	var tokens []token.Token
	var err error
	if cancellable {
		tokens, err = lexer.TokenizeContext(ctx, content)
	} else {
		tokens = lexer.Tokenize(content)
	}
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]tokenCacheEntry)
	}
	if existing := c.entries[uri]; existing.tokens != nil && existing.hash == hash {
		tokens = existing.tokens
	} else {
		c.entries[uri] = tokenCacheEntry{hash: hash, tokens: tokens}
	}
	c.mu.Unlock()
	return tokens, nil
}
