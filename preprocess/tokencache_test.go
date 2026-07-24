package preprocess

import "testing"

func TestTokenCacheReusesTokensForUnchangedContent(t *testing.T) {
	cache := NewTokenCache()
	content := []byte("stock Foo() { return 1; }\n")
	first := cache.tokenize("helper.inc", content)
	second := cache.tokenize("helper.inc", content)
	if &first[0] != &second[0] {
		t.Fatal("expected the same token slice to be reused for unchanged content")
	}
}

func TestTokenCacheInvalidatesOnContentChange(t *testing.T) {
	cache := NewTokenCache()
	first := cache.tokenize("helper.inc", []byte("stock Foo() { return 1; }\n"))
	second := cache.tokenize("helper.inc", []byte("stock Foo() { return 2; }\n"))
	if len(first) != len(second) {
		t.Fatal("expected same token count for same-shape content")
	}
	if &first[0] == &second[0] {
		t.Fatal("expected changed content to produce a fresh token slice")
	}
}

func TestNilTokenCacheTokenizesDirectly(t *testing.T) {
	var cache *TokenCache
	tokens := cache.tokenize("helper.inc", []byte("stock Foo() {}\n"))
	if len(tokens) == 0 {
		t.Fatal("expected a nil cache to still tokenize content")
	}
}
