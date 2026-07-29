package symbol

import "github.com/pawnkit/pawnkit-core/source"

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
