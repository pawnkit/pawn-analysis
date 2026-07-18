package sema

import (
	"fmt"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/diagnostic"
)

// CheckConstantOrder reports constants used before their declaration.
func CheckConstantOrder(root parser.SyntaxNode, table *symbol.Table) []diagnostic.Diagnostic {
	if !root.Valid() || table == nil {
		return nil
	}
	var diagnostics []diagnostic.Diagnostic
	walkSyntax(root, func(node parser.SyntaxNode) {
		var name, expression parser.SyntaxNode
		var ok bool
		switch node.Kind() {
		case parser.KindVariableDeclarator:
			name, ok = node.Field("name")
			if !ok {
				return
			}
			item, declared := declaredSymbol(table, name)
			if !declared || !item.IsConst {
				return
			}
			expression, ok = node.Field("initializer")
		case parser.KindEnumEntry:
			name, ok = node.Field("name")
			if ok {
				expression, ok = node.Field("value")
			}
		default:
			return
		}
		if !ok {
			return
		}
		declaration := name.Range().Span(table.File)
		walkSyntax(expression, func(identifier parser.SyntaxNode) {
			if identifier.Kind() != parser.KindIdentifier {
				return
			}
			item, found := referencedSymbol(table, identifier)
			if !found || !item.IsConst || item.Span.Start < declaration.Start {
				return
			}
			diagnostics = append(diagnostics, diagnostic.New(
				"pawn-analysis:sema/constant-before-declaration", "pawn-analysis", diagnostic.SeverityError,
				fmt.Sprintf("constant %q is used before its declaration", item.Name),
				identifier.Range().Span(table.File),
			))
		})
	})
	return diagnostics
}
