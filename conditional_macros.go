package analysis

import (
	"sort"

	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawn-parser/token"
)

func originalConditionalMacroEnds(pre *preprocess.Result) map[int]int {
	if pre.Truncated {
		return nil
	}
	var ends map[int]int
	original := pre.OriginalTokens
	expanded := pre.ExpandedTokens
	for _, invocation := range pre.MacroInvocations {
		start, end := invocation.Range.Start, invocation.Range.End
		emitted := invocation.Expansion
		if invocation.File != 0 || emitted.Start >= emitted.End || end-start < len("else") ||
			end > len(pre.Source) || string(pre.Source[end-len("else"):end]) != "else" {
			continue
		}
		next := sort.Search(len(original), func(i int) bool { return original[i].Start.Offset >= end })
		if next == 0 || original[next-1].Kind != token.KwElse || original[next-1].End.Offset != end {
			continue
		}
		first := sort.Search(len(expanded), func(i int) bool { return expanded[i].Start.Offset >= emitted.Start })
		last := sort.Search(len(expanded), func(i int) bool { return expanded[i].Start.Offset >= emitted.End })
		if !endsInConditionalHeader(expanded[first:last]) {
			continue
		}
		if ends == nil {
			ends = make(map[int]int)
		}
		ends[start] = end
	}
	return ends
}

// Look for a final if header without its body. A macro may emit declarations
// and ordinary expressions ahead of the header. Leave other control flow to
// the expanded parser rather than assuming it reaches the final if.
func endsInConditionalHeader(tokens []token.Token) bool {
	if len(tokens) < 4 || tokens[len(tokens)-1].Kind != token.RParen {
		return false
	}
	depth := 1
	index := len(tokens) - 2
	for ; index >= 0; index-- {
		switch tokens[index].Kind {
		case token.RParen:
			depth++
		case token.LParen:
			depth--
		}
		if depth == 0 {
			break
		}
	}
	header := index - 1
	if header < 0 || tokens[header].Kind != token.KwIf ||
		(header > 0 && tokens[header-1].Kind != token.Semicolon) {
		return false
	}
	for _, item := range tokens[:header] {
		switch item.Kind {
		case token.KwIf, token.KwElse, token.KwSwitch, token.KwWhile, token.KwFor, token.KwDo,
			token.KwReturn, token.KwBreak, token.KwContinue, token.KwGoto, token.LBrace, token.RBrace:
			return false
		}
	}
	return true
}
