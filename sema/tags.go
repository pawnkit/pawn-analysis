package sema

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawn-parser/token"
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

// TagCache stores immutable function results between document revisions.
type TagCache struct {
	exports  [32]byte
	revision [32]byte
	entries  map[symbol.StableID]cachedTags
}

type cachedTags struct {
	body        [32]byte
	start       source.Offset
	diagnostics []diagnostic.Diagnostic
}

const tagCacheMinimumBytes = 128

// CheckTags reports confirmed assignment and return tag mismatches.
func CheckTags(root parser.SyntaxNode, table *symbol.Table, resolver Resolver) []diagnostic.Diagnostic {
	diagnostics, _, _ := CheckTagsCached(root, table, resolver, nil, "")
	return diagnostics
}

// CheckTagsCached reuses tag checks for unchanged functions.
func CheckTagsCached(
	root parser.SyntaxNode,
	table *symbol.Table,
	resolver Resolver,
	previous *TagCache,
	revision string,
) ([]diagnostic.Diagnostic, *TagCache, int) {
	if !root.Valid() || table == nil {
		return nil, &TagCache{entries: make(map[symbol.StableID]cachedTags)}, 0
	}
	if !hasCacheableTagFunction(root) {
		checker := tagChecker{table: table, resolver: resolver}
		checker.walk(root, "")
		return checker.diagnostics, &TagCache{entries: make(map[symbol.StableID]cachedTags)}, 0
	}
	exports := table.ExportFingerprint()
	revisionHash := sha256.Sum256([]byte(revision))
	next := &TagCache{
		exports: exports, revision: revisionHash,
		entries: make(map[symbol.StableID]cachedTags),
	}
	canReuse := previous != nil && previous.exports == exports && previous.revision == revisionHash
	checker := tagChecker{table: table, resolver: resolver}
	reused := 0
	declarations := root.Declarations()
	for declarations.Next() {
		declaration := declarations.Declaration()
		if declaration.Kind() != parser.KindFunctionDefinition {
			checker.walk(declaration, "")
			continue
		}
		name, ok := declaration.Field("name")
		if !ok {
			checker.walk(declaration, "")
			continue
		}
		callable, ok := declaredSymbol(table, name)
		if !ok {
			checker.walk(declaration, "")
			continue
		}
		stableID, stable := table.StableSymbolID(callable.ID)
		cacheable := stable && len(declaration.Bytes()) >= tagCacheMinimumBytes
		var bodyHash [32]byte
		if cacheable {
			bodyHash = sha256.Sum256(declaration.Bytes())
		}
		if canReuse && cacheable {
			if cached, found := previous.entries[stableID]; found && cached.body == bodyHash {
				items := shiftTagDiagnostics(cached.diagnostics, table.File, callable.Span.Start-cached.start)
				checker.diagnostics = append(checker.diagnostics, items...)
				next.entries[stableID] = cachedTags{
					body: bodyHash, start: callable.Span.Start, diagnostics: items,
				}
				reused++
				continue
			}
		}
		firstDiagnostic := len(checker.diagnostics)
		checker.walk(declaration, "")
		if cacheable {
			next.entries[stableID] = cachedTags{
				body: bodyHash, start: callable.Span.Start,
				diagnostics: append([]diagnostic.Diagnostic(nil), checker.diagnostics[firstDiagnostic:]...),
			}
		}
	}
	return checker.diagnostics, next, reused
}

func hasCacheableTagFunction(root parser.SyntaxNode) bool {
	declarations := root.Declarations()
	for declarations.Next() {
		declaration := declarations.Declaration()
		if declaration.Kind() == parser.KindFunctionDefinition && len(declaration.Bytes()) >= tagCacheMinimumBytes {
			return true
		}
	}
	return false
}

func shiftTagDiagnostics(items []diagnostic.Diagnostic, file source.FileID, delta source.Offset) []diagnostic.Diagnostic {
	if len(items) == 0 {
		return nil
	}
	result := append([]diagnostic.Diagnostic(nil), items...)
	for i := range result {
		result[i].Primary.File = file
		result[i].Primary.Start += delta
		result[i].Primary.End += delta
		result[i].Related = append([]diagnostic.RelatedLocation(nil), result[i].Related...)
		for j := range result[i].Related {
			result[i].Related[j].Span.File = file
			result[i].Related[j].Span.Start += delta
			result[i].Related[j].Span.End += delta
		}
	}
	return result
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
		if tag == "_" {
			return true
		}
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
	return c.table.DeclarationAt(span)
}

func (c *tagChecker) reference(node parser.SyntaxNode) (symbol.Symbol, bool) {
	span := node.Range().Span(c.table.File)
	if item, ok := c.table.ReferencedAt(span); ok {
		return item, true
	}
	return c.declaration(node)
}
