package sema

import (
	"context"
	"fmt"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/diagnostic"
)

// CheckConstantOrder reports constants used before their declaration.
func CheckConstantOrder(root parser.SyntaxNode, table *symbol.Table) []diagnostic.Diagnostic {
	diagnostics, _ := checkConstantOrder(context.Background(), false, root, table)
	return diagnostics
}

// CheckConstantOrderContext reports ordering errors and observes cancellation.
func CheckConstantOrderContext(
	ctx context.Context,
	root parser.SyntaxNode,
	table *symbol.Table,
) ([]diagnostic.Diagnostic, error) {
	return checkConstantOrder(ctx, true, root, table)
}

func checkConstantOrder(
	ctx context.Context,
	cancellable bool,
	root parser.SyntaxNode,
	table *symbol.Table,
) ([]diagnostic.Diagnostic, error) {
	cancel := cancellation{ctx: ctx, cancellable: cancellable}
	if cancellable {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if !root.Valid() || table == nil {
		return nil, nil
	}
	var diagnostics []diagnostic.Diagnostic
	walkSyntaxContext(root, &cancel, func(node parser.SyntaxNode) {
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
		walkSyntaxContext(expression, &cancel, func(identifier parser.SyntaxNode) {
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
	if cancel.err != nil {
		return nil, cancel.err
	}
	return diagnostics, nil
}
