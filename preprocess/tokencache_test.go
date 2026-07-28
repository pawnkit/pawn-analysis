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

func TestResultRetainsTokensForEachFile(t *testing.T) {
	cache := NewTokenCache()
	include := []byte("stock Helper() {}\n")
	result := Run([]byte("#include \"helper.inc\"\n"), Options{
		Resolver:   MapResolver{"helper.inc": include},
		TokenCache: cache,
	})
	if len(result.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(result.Files))
	}
	for _, file := range result.Files {
		if len(file.Tokens) == 0 {
			t.Fatalf("%s has no tokens", file.URI)
		}
	}
	cached := cache.tokenize("helper.inc", include)
	if &result.Files[1].Tokens[0] != &cached[0] {
		t.Fatal("include tokens were copied instead of shared")
	}
}
