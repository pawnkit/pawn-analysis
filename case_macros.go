package analysis

import (
	"sort"

	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawn-parser/token"
)

func originalCaseMacroEnds(pre *preprocess.Result) map[int]int {
	if pre.Truncated {
		return nil
	}
	var ends map[int]int
	tokens := pre.OriginalTokens
	for _, inv := range pre.MacroInvocations {
		start, end := inv.Range.Start, inv.Range.End
		if inv.File != 0 || end <= start || end > len(pre.Source) || pre.Source[end-1] != '>' {
			continue
		}
		index := sort.Search(len(tokens), func(i int) bool { return tokens[i].Start.Offset >= start })
		if index+1 >= len(tokens) || tokens[index].Start.Offset != start ||
			tokens[index].Kind != token.Identifier || tokens[index+1].Kind != token.Lt {
			continue
		}
		if ends == nil {
			ends = make(map[int]int)
		}
		ends[start] = max(ends[start], end)
	}
	return ends
}
