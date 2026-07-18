package sema

import (
	"fmt"
	"strings"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawn-parser/token"
	"github.com/pawnkit/pawnkit-core/diagnostic"
)

// CheckTags reports confirmed assignment and return tag mismatches.
func CheckTags(root parser.SyntaxNode, table *symbol.Table, resolver Resolver) []diagnostic.Diagnostic {
	if !root.Valid() || table == nil {
		return nil
	}
	c := tagChecker{table: table, resolver: resolver}
	c.walk(root, "")
	return c.diagnostics
}

type tagChecker struct {
	table       *symbol.Table
	resolver    Resolver
	diagnostics []diagnostic.Diagnostic
}

func (c *tagChecker) walk(node parser.SyntaxNode, returnTag string) {
	if !node.Valid() {
		return
	}
	switch node.Kind() {
	case parser.KindFunctionDefinition:
		if name, ok := node.Field("name"); ok {
			if item, found := c.declaration(name); found {
				returnTag = item.Tag
			}
		}
	case parser.KindVariableDeclarator:
		c.checkInitializer(node)
	case parser.KindAssignmentExpression:
		if _, ok := c.operatorResult(node); !ok {
			c.checkPair(node, "left", "right", "assignment")
		}
	case parser.KindBinaryExpression:
		if _, ok := c.operatorResult(node); !ok {
			c.checkPair(node, "left", "right", "binary expression")
		}
	case parser.KindTernaryExpression:
		c.checkPair(node, "consequence", "alternative", "ternary expression")
	case parser.KindCallExpression:
		c.checkCall(node)
	case parser.KindReturnStatement:
		if value, ok := node.Field("value"); ok {
			c.checkExpected(value, returnTag, "return value")
		}
	}
	it := node.Children()
	for it.Next() {
		c.walk(it.Node(), returnTag)
	}
}

func (c *tagChecker) checkCall(node parser.SyntaxNode) {
	function, functionOK := node.Field("function")
	arguments, argumentsOK := node.Field("arguments")
	if !functionOK || !argumentsOK {
		return
	}
	callable, ok := c.reference(function)
	var tags []string
	if ok && callable.Kind.IsCallable() {
		tags = callable.ParamTags
	} else if external, found := c.externalCallable(function.Text()); found {
		tags = external.ParamTags
	} else {
		return
	}
	it := arguments.Children()
	index := 0
	for it.Next() {
		argument := it.Node()
		if index < len(tags) {
			c.checkExpected(argument, tags[index], fmt.Sprintf("argument %d to %s", index+1, function.Text()))
		}
		index++
	}
}

func (c *tagChecker) checkInitializer(node parser.SyntaxNode) {
	name, nameOK := node.Field("name")
	value, valueOK := node.Field("initializer")
	if !nameOK || !valueOK {
		return
	}
	item, ok := c.declaration(name)
	if ok {
		c.checkExpected(value, item.Tag, "initializer")
	}
}

func (c *tagChecker) checkPair(node parser.SyntaxNode, leftField, rightField, context string) {
	left, leftOK := node.Field(leftField)
	right, rightOK := node.Field(rightField)
	if !leftOK || !rightOK {
		return
	}
	expected, expectedOK := c.tag(left)
	actual, actualOK := c.tag(right)
	if expectedOK && actualOK && !tagsCompatible(expected, actual) {
		c.mismatch(right, expected, actual, context)
	}
}

func (c *tagChecker) checkExpected(node parser.SyntaxNode, expected, context string) {
	if expected == "" {
		return
	}
	actual, ok := c.tag(node)
	if ok && !tagsCompatible(expected, actual) {
		c.mismatch(node, expected, actual, context)
	}
}

func tagsCompatible(expected, actual string) bool {
	if expected == "" || actual == "" || expected == "_" || actual == "_" {
		return true
	}
	accepted := make(map[string]struct{})
	for _, tag := range strings.Split(expected, "|") {
		accepted[tag] = struct{}{}
	}
	for _, tag := range strings.Split(actual, "|") {
		if _, ok := accepted[tag]; ok {
			return true
		}
	}
	return false
}

func (c *tagChecker) mismatch(node parser.SyntaxNode, expected, actual, context string) {
	c.diagnostics = append(c.diagnostics, diagnostic.New(
		"pawn-analysis:sema/tag-mismatch", "pawn-analysis", diagnostic.SeverityWarning,
		fmt.Sprintf("%s expects tag %s, got %s", context, expected, actual),
		node.Range().Span(c.table.File),
	))
}

func (c *tagChecker) tag(node parser.SyntaxNode) (string, bool) {
	switch node.Kind() {
	case parser.KindIdentifier:
		item, ok := c.reference(node)
		return item.Tag, ok && item.Tag != ""
	case parser.KindLiteral:
		if node.Token().Kind() == token.FloatLiteral {
			return "Float", true
		}
	case parser.KindTaggedExpression:
		if tag, ok := node.Field("tag"); ok && tag.Text() != "" {
			return tag.Text(), true
		}
	case parser.KindCallExpression:
		if fn, ok := node.Field("function"); ok {
			item, found := c.reference(fn)
			if found && item.Tag != "" {
				return item.Tag, true
			}
			if external, found := c.externalCallable(fn.Text()); found && external.ReturnTag != "" {
				return external.ReturnTag, true
			}
		}
	case parser.KindParenthesizedExpression, parser.KindUnaryExpression:
		if inner, ok := node.Field("expression"); ok {
			if node.Kind() == parser.KindUnaryExpression {
				if result, found := c.operatorResult(node); found {
					return result, result != ""
				}
				if node.Token().Kind() == token.Bang {
					return "bool", true
				}
			}
			return c.tag(inner)
		}
	case parser.KindBinaryExpression:
		if result, found := c.operatorResult(node); found {
			return result, result != ""
		}
		if isBooleanOperator(node.Token().Kind()) {
			return "bool", true
		}
		left, leftOK := node.Field("left")
		right, rightOK := node.Field("right")
		if leftOK && rightOK {
			leftTag, lok := c.tag(left)
			rightTag, rok := c.tag(right)
			if lok && rok && leftTag == rightTag {
				return leftTag, true
			}
		}
	case parser.KindTernaryExpression:
		left, leftOK := node.Field("consequence")
		right, rightOK := node.Field("alternative")
		if leftOK && rightOK {
			leftTag, lok := c.tag(left)
			rightTag, rok := c.tag(right)
			if lok && rok && leftTag == rightTag {
				return leftTag, true
			}
		}
	}
	return "", false
}

func isBooleanOperator(kind token.Kind) bool {
	switch kind {
	case token.Eq, token.NotEq, token.Lt, token.Gt, token.LtEq, token.GtEq, token.AndAnd, token.OrOr:
		return true
	default:
		return false
	}
}

func (c *tagChecker) operatorResult(node parser.SyntaxNode) (string, bool) {
	name := "operator" + node.Token().Kind().String()
	var operands []parser.SyntaxNode
	if left, ok := node.Field("left"); ok {
		operands = append(operands, left)
	}
	if expression, ok := node.Field("expression"); ok {
		operands = append(operands, expression)
	}
	if right, ok := node.Field("right"); ok {
		operands = append(operands, right)
	}
	if len(operands) == 0 {
		return "", false
	}
	actual := make([]string, len(operands))
	for i, operand := range operands {
		tag, ok := c.tag(operand)
		if !ok {
			return "", false
		}
		actual[i] = tag
	}
	for _, overload := range c.table.OperatorOverloads(name) {
		if len(overload.ParamTags) != len(actual) {
			continue
		}
		matches := true
		for i := range actual {
			if !tagsCompatible(overload.ParamTags[i], actual[i]) {
				matches = false
				break
			}
		}
		if matches {
			return overload.Tag, true
		}
	}
	return "", false
}

func (c *tagChecker) externalCallable(name string) (Callable, bool) {
	resolver, ok := c.resolver.(CallableResolver)
	if !ok {
		return Callable{}, false
	}
	return resolver.ResolveCallable(name)
}

func (c *tagChecker) declaration(node parser.SyntaxNode) (symbol.Symbol, bool) {
	span := node.Range().Span(c.table.File)
	for _, item := range c.table.Symbols {
		if item.Span == span {
			return item, true
		}
	}
	return symbol.Symbol{}, false
}

func (c *tagChecker) reference(node parser.SyntaxNode) (symbol.Symbol, bool) {
	span := node.Range().Span(c.table.File)
	for _, ref := range c.table.References {
		if ref.Span == span && ref.Resolved != 0 {
			return c.table.Symbol(ref.Resolved)
		}
	}
	return c.declaration(node)
}
