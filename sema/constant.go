package sema

import (
	"context"
	"strconv"
	"strings"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawn-parser/token"
)

type Constant struct {
	Value int64
	Known bool
}

// EvalConstant evaluates a supported constant expression.
func EvalConstant(node parser.SyntaxNode) Constant {
	return EvalConstantResolved(node, nil)
}

// EvalConstantResolved evaluates an expression with optional identifier lookup.
func EvalConstantResolved(node parser.SyntaxNode, resolve func(parser.SyntaxNode) Constant) Constant {
	return constantEvaluator{resolve: resolve}.eval(node)
}

type constantEvaluator struct {
	resolve func(parser.SyntaxNode) Constant
}

func (e constantEvaluator) eval(node parser.SyntaxNode) Constant {
	if !node.Valid() || node.HasError() {
		return Constant{}
	}
	switch node.Kind() {
	case parser.KindIdentifier:
		if e.resolve != nil {
			return e.resolve(node)
		}
	case parser.KindLiteral:
		return parseInteger(node.Text())
	case parser.KindParenthesizedExpression, parser.KindTaggedExpression:
		if inner, ok := node.Field("expression"); ok {
			return e.eval(inner)
		}
	case parser.KindUnaryExpression:
		return e.evalUnary(node)
	case parser.KindBinaryExpression:
		return e.evalBinary(node)
	case parser.KindTernaryExpression:
		condition, a := node.Field("condition")
		consequence, b := node.Field("consequence")
		alternative, c := node.Field("alternative")
		if a && b && c {
			value := e.eval(condition)
			if value.Known && value.Value != 0 {
				return e.eval(consequence)
			}
			if value.Known {
				return e.eval(alternative)
			}
		}
	}
	return Constant{}
}

func parseInteger(text string) Constant {
	value, err := strconv.ParseInt(strings.ReplaceAll(text, "_", ""), 0, 64)
	if err != nil {
		return Constant{}
	}
	return Constant{Value: value, Known: true}
}

func (e constantEvaluator) evalUnary(node parser.SyntaxNode) Constant {
	inner, ok := node.Field("expression")
	if !ok {
		return Constant{}
	}
	value := e.eval(inner)
	if !value.Known {
		return value
	}
	switch node.Token().Kind() {
	case token.Plus:
		return value
	case token.Minus:
		value.Value = -value.Value
	case token.Tilde:
		value.Value = ^value.Value
	case token.Bang:
		value.Value = boolCell(value.Value == 0)
	default:
		return Constant{}
	}
	return value
}

func (e constantEvaluator) evalBinary(node parser.SyntaxNode) Constant {
	leftNode, leftOK := node.Field("left")
	rightNode, rightOK := node.Field("right")
	if !leftOK || !rightOK {
		return Constant{}
	}
	left, right := e.eval(leftNode), e.eval(rightNode)
	if !left.Known || !right.Known {
		return Constant{}
	}
	var value int64
	switch node.Token().Kind() {
	case token.Plus:
		value = left.Value + right.Value
	case token.Minus:
		value = left.Value - right.Value
	case token.Star:
		value = left.Value * right.Value
	case token.Slash:
		if right.Value == 0 {
			return Constant{}
		}
		value = left.Value / right.Value
	case token.Percent:
		if right.Value == 0 {
			return Constant{}
		}
		value = left.Value % right.Value
	case token.Shl:
		value = left.Value << uint64(right.Value)
	case token.Shr, token.Ushr:
		value = left.Value >> uint64(right.Value)
	case token.Amp:
		value = left.Value & right.Value
	case token.Pipe:
		value = left.Value | right.Value
	case token.Caret:
		value = left.Value ^ right.Value
	case token.Eq:
		value = boolCell(left.Value == right.Value)
	case token.NotEq:
		value = boolCell(left.Value != right.Value)
	case token.Lt:
		value = boolCell(left.Value < right.Value)
	case token.LtEq:
		value = boolCell(left.Value <= right.Value)
	case token.Gt:
		value = boolCell(left.Value > right.Value)
	case token.GtEq:
		value = boolCell(left.Value >= right.Value)
	case token.AndAnd:
		value = boolCell(left.Value != 0 && right.Value != 0)
	case token.OrOr:
		value = boolCell(left.Value != 0 || right.Value != 0)
	default:
		return Constant{}
	}
	return Constant{Value: value, Known: true}
}

type constantCandidate struct {
	expression parser.SyntaxNode
	previous   symbol.ID
	implicit   bool
}

// ResolveConstants evaluates const declarations and enum members.
func ResolveConstants(root parser.SyntaxNode, table *symbol.Table) map[symbol.ID]Constant {
	values, _ := resolveConstants(context.Background(), false, root, table)
	return values
}

// ResolveConstantsContext evaluates constants and observes cancellation.
func ResolveConstantsContext(
	ctx context.Context,
	root parser.SyntaxNode,
	table *symbol.Table,
) (map[symbol.ID]Constant, error) {
	return resolveConstants(ctx, true, root, table)
}

func resolveConstants(
	ctx context.Context,
	cancellable bool,
	root parser.SyntaxNode,
	table *symbol.Table,
) (map[symbol.ID]Constant, error) {
	values := make(map[symbol.ID]Constant)
	cancel := cancellation{ctx: ctx, cancellable: cancellable}
	if cancellable {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if !root.Valid() || table == nil {
		return values, nil
	}
	candidates := make(map[symbol.ID]constantCandidate)
	collectConstantCandidates(root, table, candidates, &cancel)
	if cancel.err != nil {
		return nil, cancel.err
	}
	visiting := make(map[symbol.ID]bool)
	var value func(symbol.ID) Constant
	value = func(id symbol.ID) Constant {
		if cancel.err != nil || cancel.poll() {
			return Constant{}
		}
		if known, ok := values[id]; ok {
			return known
		}
		candidate, ok := candidates[id]
		if !ok || visiting[id] {
			return Constant{}
		}
		visiting[id] = true
		var result Constant
		if candidate.implicit {
			if candidate.previous == 0 {
				result = Constant{Known: true}
			} else if previous := value(candidate.previous); previous.Known {
				result = Constant{Value: previous.Value + 1, Known: true}
			}
		} else {
			result = EvalConstantResolved(candidate.expression, func(node parser.SyntaxNode) Constant {
				if cancel.poll() {
					return Constant{}
				}
				if ref, ok := referencedSymbol(table, node); ok {
					return value(ref.ID)
				}
				return Constant{}
			})
		}
		delete(visiting, id)
		if result.Known {
			values[id] = result
		}
		return result
	}
	for id := range candidates {
		if cancel.poll() {
			return nil, cancel.err
		}
		value(id)
	}
	if cancel.err != nil {
		return nil, cancel.err
	}
	return values, nil
}

func collectConstantCandidates(
	node parser.SyntaxNode,
	table *symbol.Table,
	out map[symbol.ID]constantCandidate,
	cancel *cancellation,
) {
	if cancel.err != nil || cancel.poll() {
		return
	}
	if node.Kind() == parser.KindVariableDeclarator {
		name, nameOK := node.Field("name")
		expression, valueOK := node.Field("initializer")
		if nameOK && valueOK {
			if item, ok := declaredSymbol(table, name); ok && item.IsConst {
				out[item.ID] = constantCandidate{expression: expression}
			}
		}
	}
	if node.Kind() == parser.KindEnumDeclaration {
		collectEnumCandidates(node, table, out, cancel)
		return
	}
	it := node.Children()
	for it.Next() {
		collectConstantCandidates(it.Node(), table, out, cancel)
	}
}

func collectEnumCandidates(
	node parser.SyntaxNode,
	table *symbol.Table,
	out map[symbol.ID]constantCandidate,
	cancel *cancellation,
) {
	body, ok := node.Field("body")
	if !ok {
		return
	}
	var previous symbol.ID
	it := body.Children()
	for it.Next() {
		if cancel.poll() {
			return
		}
		entry := it.Node()
		if entry.Kind() != parser.KindEnumEntry {
			continue
		}
		name, ok := entry.Field("name")
		if !ok {
			continue
		}
		item, ok := declaredSymbol(table, name)
		if !ok {
			continue
		}
		if expression, explicit := entry.Field("value"); explicit {
			out[item.ID] = constantCandidate{expression: expression}
		} else {
			out[item.ID] = constantCandidate{previous: previous, implicit: true}
		}
		previous = item.ID
	}
}

func referencedSymbol(table *symbol.Table, node parser.SyntaxNode) (symbol.Symbol, bool) {
	return table.ReferencedAt(node.Range().Span(table.File))
}

func boolCell(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
