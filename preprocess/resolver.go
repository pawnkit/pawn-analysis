package preprocess

// IncludeResolver resolves #include/#tryinclude targets to content. A host
// tool supplies an implementation backed by pawn-project's include search
// (or an in-memory map for tests); this package performs no filesystem
// access itself, per the architecture's narrow-interface boundary rule.
type IncludeResolver interface {
	// Resolve looks up path as referenced from the file identified by
	// fromURI. angle reports whether the directive used <path> (system
	// search) rather than "path" (quoted, relative-first search).
	//
	// ok is false when the target could not be found; Run treats that as an
	// error for #include and as a silent no-op for #tryinclude.
	Resolve(fromURI, path string, angle bool) (content []byte, resolvedURI string, ok bool)
}

// MapResolver is a trivial [IncludeResolver] backed by an in-memory map
// keyed by the exact path text used in the directive, ignoring fromURI and
// angle. Useful for tests and small embedded-include scenarios.
type MapResolver map[string][]byte

func (m MapResolver) Resolve(_, path string, _ bool) ([]byte, string, bool) {
	content, ok := m[path]
	if !ok {
		return nil, "", false
	}
	return content, path, true
}
