package preprocess

import "strings"

// MacroKind distinguishes object-like from function-like macros.
type MacroKind uint8

const (
	MacroObjectLike MacroKind = iota + 1
	MacroFunctionLike
)

func (k MacroKind) String() string {
	if k == MacroFunctionLike {
		return "function-like"
	}
	return "object-like"
}

// Macro is one #define'd macro, as currently known to the preprocessor.
// Pawn's macro parameters are positional (%0, %1, ... in the body), not
// C-style named substitution, though a parameter list may still name its
// slots for documentation; NamedParams records that mapping when present so
// a body identifier matching a declared name also substitutes.
type Macro struct {
	Name        string
	Kind        MacroKind
	ParamCount  int
	NamedParams map[string]int
	Body        []ptok
	File        uint32
	DefSpan     ByteRange
}

type macroTable struct {
	byName map[string]Macro
}

func newMacroTable() *macroTable {
	return &macroTable{byName: make(map[string]Macro)}
}

func (t *macroTable) define(m Macro) (previous Macro, redefined bool) {
	previous, redefined = t.byName[m.Name]
	t.byName[m.Name] = m
	return previous, redefined
}

func (t *macroTable) undef(name string) bool {
	_, ok := t.byName[name]
	delete(t.byName, name)
	return ok
}

func (t *macroTable) lookup(name string) (Macro, bool) {
	m, ok := t.byName[name]
	return m, ok
}

func (t *macroTable) defined(name string) bool {
	_, ok := t.byName[name]
	return ok
}

// snapshot returns an immutable copy of the current macro definitions,
// safe to retain after processing continues to mutate the table.
func (t *macroTable) snapshot() map[string]Macro {
	out := make(map[string]Macro, len(t.byName))
	for k, v := range t.byName {
		out[k] = v
	}
	return out
}

// parseParamIndex parses a MacroParam token's text ("%0".."%9", or "%%")
// into a positional argument index. ok is false for "%%", which denotes a
// literal '%' rather than a parameter reference.
func parseParamIndex(text string) (index int, ok bool) {
	if text == "%%" {
		return 0, false
	}
	digits := strings.TrimPrefix(text, "%")
	if digits == "" {
		return 0, false
	}
	n := 0
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}
