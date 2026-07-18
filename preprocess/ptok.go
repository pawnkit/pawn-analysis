package preprocess

import "github.com/pawnkit/pawn-parser/token"

// ptok pairs a token with its spelled text. Text extraction from a bare
// token.Token requires knowing which source buffer its offsets index into;
// once tokens start moving between files (via #include splicing) or being
// copied during macro substitution, that buffer is no longer implicit, so
// the expansion pipeline carries text explicitly instead of re-deriving it
// from offsets that may point into a different file's bytes.
type ptok struct {
	token.Token
	text string
	file uint32
}

func toPtok(source []byte, t token.Token, file ...uint32) ptok {
	var id uint32
	if len(file) != 0 {
		id = file[0]
	}
	return ptok{Token: t, text: t.Text(source), file: id}
}

func toPtoks(source []byte, toks []token.Token, file ...uint32) []ptok {
	out := make([]ptok, len(toks))
	for i, t := range toks {
		out[i] = toPtok(source, t, file...)
	}
	return out
}
