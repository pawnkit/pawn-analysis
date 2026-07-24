package preprocess

import (
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
	if c == nil {
		return lexer.Tokenize(content)
	}
	hash := sha256.Sum256(content)
	c.mu.RLock()
	entry, ok := c.entries[uri]
	c.mu.RUnlock()
	if ok && entry.hash == hash {
		return entry.tokens
	}
	tokens := lexer.Tokenize(content)
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
	return tokens
}
