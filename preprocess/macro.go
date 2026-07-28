package preprocess

import (
	"strings"

	"github.com/pawnkit/pawn-parser/token"
)

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

// Macro is one active #define.
type Macro struct {
	Name            string
	Kind            MacroKind
	ParamCount      int
	ParamSlots      map[int]int
	NamedParams     map[string]int
	FlexiblePattern bool
	Body            []ptok
	File            uint32
	DefSpan         ByteRange
}

// ReplacementCallable returns the function called by a forwarding macro.
func (m Macro) ReplacementCallable() (string, bool) {
	if m.FlexiblePattern {
		name, lowest := "", m.ParamCount
		for candidate, slot := range m.NamedParams {
			if slot < lowest {
				name, lowest = candidate, slot
			}
		}
		if name != "" {
			return name, true
		}
	}
	for index := 0; index < len(m.Body); index++ {
		if m.Body[index].Kind != token.Identifier {
			continue
		}
		next := index + 1
		for next < len(m.Body) && m.Body[next].Kind.IsTrivia() {
			next++
		}
		if next < len(m.Body) && m.Body[next].Kind == token.LParen {
			return m.Body[index].text, true
		}
	}
	return "", false
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

// parseParamIndex parses a positional label such as "%0".
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
