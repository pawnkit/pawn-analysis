package sema

import (
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawn-parser/token"
)

// The parser stores a comparison chain as left-associated binary nodes. Pawn
// compares adjacent operands. A parenthesized comparison is a separate value.
func relationalChainLeft(node, left parser.SyntaxNode) (parser.SyntaxNode, bool) {
	if node.Kind() == parser.KindBinaryExpression && isRelationalOperator(node.Token().Kind()) &&
		left.Kind() == parser.KindBinaryExpression && isRelationalOperator(left.Token().Kind()) {
		if middle, ok := left.Field("right"); ok {
			return middle, true
		}
	}
	return left, false
}

func isRelationalOperator(kind token.Kind) bool {
	switch kind {
	case token.Lt, token.LtEq, token.Gt, token.GtEq:
		return true
	default:
		return false
	}
}
