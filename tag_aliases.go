package analysis

import (
	"strings"
	"unicode"

	"github.com/pawnkit/pawn-parser/preprocess"
)

const maxTagAliasDepth = 32

func resolveTagMacros(text string, macros map[string]preprocess.Macro) string {
	if text == "" || len(macros) == 0 {
		return text
	}
	return expandTagMacros(text, macros, make(map[string]bool), 0)
}

func expandTagMacros(text string, macros map[string]preprocess.Macro, active map[string]bool, depth int) string {
	if depth >= maxTagAliasDepth {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if !isTagIdentifierStart(text[i]) {
			out.WriteByte(text[i])
			i++
			continue
		}
		start := i
		i++
		for i < len(text) && isTagIdentifierPart(text[i]) {
			i++
		}
		name := text[start:i]
		macro, ok := macros[name]
		if !ok || macro.Kind != preprocess.MacroObjectLike || active[name] {
			out.WriteString(name)
			continue
		}
		active[name] = true
		out.WriteString(expandTagMacros(macro.BodyText, macros, active, depth+1))
		delete(active, name)
	}
	return out.String()
}

func isTagIdentifierStart(char byte) bool {
	return char == '_' || unicode.IsLetter(rune(char))
}

func isTagIdentifierPart(char byte) bool {
	return isTagIdentifierStart(char) || char >= '0' && char <= '9'
}
