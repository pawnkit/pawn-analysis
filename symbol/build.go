package symbol

import (
	"context"
	"fmt"
	"strings"

	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawn-parser/token"
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

// Build creates a symbol table from an error-tolerant syntax tree.
func Build(root parser.SyntaxNode, file source.FileID) *Table {
	return BuildMapped(root, file, nil)
}

// BuildContext creates a symbol table and stops when ctx is cancelled.
func BuildContext(ctx context.Context, root parser.SyntaxNode, file source.FileID) (*Table, error) {
	return build(ctx, true, root, file, nil)
}

// BuildMapped maps token provenance file indexes to shared file IDs.
func BuildMapped(root parser.SyntaxNode, file source.FileID, files func(uint32) source.FileID) *Table {
	table, _ := build(context.Background(), false, root, file, files)
	return table
}

// BuildMappedContext maps token origins and stops when ctx is cancelled.
func BuildMappedContext(
	ctx context.Context,
	root parser.SyntaxNode,
	file source.FileID,
	files func(uint32) source.FileID,
) (*Table, error) {
	return build(ctx, true, root, file, files)
}

// BuildMappedDeclarationsContext builds mapped top-level declarations.
func BuildMappedDeclarationsContext(
	ctx context.Context,
	root parser.SyntaxNode,
	file source.FileID,
	files func(uint32) source.FileID,
) (*Table, error) {
	return buildDeclarations(ctx, root, file, files, nil, false)
}

// BuildMappedNavigationContext keeps details for the active file.
func BuildMappedNavigationContext(
	ctx context.Context,
	root parser.SyntaxNode,
	file source.FileID,
	files func(uint32) source.FileID,
) (*Table, error) {
	return buildDeclarations(ctx, root, file, files, nil, true)
}

// BuildMappedNavigationWithSpansContext keeps active-file details and uses
// mapSpan to map expanded nodes back to source files.
func BuildMappedNavigationWithSpansContext(
	ctx context.Context,
	root parser.SyntaxNode,
	file source.FileID,
	mapSpan func(parser.SyntaxNode) (source.Span, bool),
) (*Table, error) {
	return buildDeclarations(ctx, root, file, nil, mapSpan, true)
}

// BuildMappedWithSpansContext builds full symbols using mapped source spans.
func BuildMappedWithSpansContext(
	ctx context.Context,
	root parser.SyntaxNode,
	file source.FileID,
	mapSpan func(parser.SyntaxNode) (source.Span, bool),
) (*Table, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := &builder{
		ctx: ctx, cancellable: true, file: file, mapSpan: mapSpan, table: &Table{File: file},
	}
	fileScope := b.newScope(ScopeFile, 0)
	decls := root.Declarations()
	for decls.Next() {
		b.walk(fileScope, decls.Declaration())
		if b.cancelled != nil {
			return nil, b.cancelled
		}
	}
	b.resolveFileReferences(fileScope)
	b.buildSpanIndexes()
	if b.cancelled != nil {
		return nil, b.cancelled
	}
	return b.table, nil
}

func buildDeclarations(
	ctx context.Context,
	root parser.SyntaxNode,
	file source.FileID,
	files func(uint32) source.FileID,
	mapSpan func(parser.SyntaxNode) (source.Span, bool),
	rootDetails bool,
) (*Table, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := &builder{
		ctx: ctx, cancellable: true, declarationsOnly: true, rootDetails: rootDetails,
		file: file, files: files, mapSpan: mapSpan, table: &Table{File: file},
	}
	fileScope := b.newScope(ScopeFile, 0)
	decls := root.Declarations()
	for decls.Next() {
		b.walk(fileScope, decls.Declaration())
		if b.cancelled != nil {
			return nil, b.cancelled
		}
	}
	if rootDetails {
		b.resolveFileReferences(fileScope)
	}
	b.buildSpanIndexes()
	if b.cancelled != nil {
		return nil, b.cancelled
	}
	return b.table, nil
}

func build(
	ctx context.Context,
	cancellable bool,
	root parser.SyntaxNode,
	file source.FileID,
	files func(uint32) source.FileID,
) (*Table, error) {
	if cancellable {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	b := &builder{ctx: ctx, cancellable: cancellable, file: file, files: files, table: &Table{File: file}}
	fileScope := b.newScope(ScopeFile, 0)

	decls := root.Declarations()
	for decls.Next() {
		b.walk(fileScope, decls.Declaration())
		if b.cancelled != nil {
			return nil, b.cancelled
		}
	}
	b.resolveFileReferences(fileScope)
	b.buildSpanIndexes()
	if b.cancelled != nil {
		return nil, b.cancelled
	}
	return b.table, nil
}

func (b *builder) buildSpanIndexes() {
	b.table.declarations = make(map[source.Span]ID, len(b.table.Symbols))
	for _, item := range b.table.Symbols {
		if b.pollCancellation() {
			return
		}
		if b.table.declarations[item.Span] == 0 {
			b.table.declarations[item.Span] = item.ID
		}
	}
	b.table.references = make(map[source.Span]ID, len(b.table.References))
	for _, reference := range b.table.References {
		if b.pollCancellation() {
			return
		}
		if reference.Resolved != 0 && b.table.references[reference.Span] == 0 {
			b.table.references[reference.Span] = reference.Resolved
		}
	}
}

type builder struct {
	ctx              context.Context
	cancellable      bool
	cancelled        error
	steps            uint32
	declarationsOnly bool
	rootDetails      bool
	file             source.FileID
	files            func(uint32) source.FileID
	mapSpan          func(parser.SyntaxNode) (source.Span, bool)
	table            *Table
}

func (b *builder) keepDetails(span source.Span) bool {
	return !b.declarationsOnly || b.rootDetails && span.File == b.file
}

func (b *builder) pollCancellation() bool {
	if !b.cancellable {
		return false
	}
	b.steps++
	if b.steps%256 != 0 {
		return false
	}
	if err := b.ctx.Err(); err != nil {
		b.cancelled = err
		return true
	}
	return false
}

func (b *builder) newScope(kind ScopeKind, parent ID) ID {
	id := ID(len(b.table.Scopes) + 1)
	b.table.Scopes = append(b.table.Scopes, Scope{ID: id, Kind: kind, Parent: parent, Names: make(map[string]ID)})
	return id
}

func (b *builder) appendSymbol(sym Symbol) ID {
	id := ID(len(b.table.Symbols) + 1)
	sym.ID = id
	b.table.Symbols = append(b.table.Symbols, sym)
	return id
}

func (b *builder) declare(scope ID, sym Symbol) ID {
	sym.Scope = scope
	sc := &b.table.Scopes[scope-1]
	if existingID, ok := sc.Names[sym.Name]; ok {
		existing, _ := b.table.Symbol(existingID)
		id := b.appendSymbol(sym)
		if IsTestEntryPoint(sym) {
			return id
		}
		if isForwardPair(existing.Kind, sym.Kind) {
			if existing.Kind == KindForward {
				sc.Names[sym.Name] = id
			}
			return id
		}
		if existing.Kind.IsCallable() && sym.Kind.IsCallable() && existing.StateSelector != "" && sym.StateSelector != "" {
			return id
		}
		d := diagnostic.New(
			"pawn-analysis:symbol/redeclared", "pawn-analysis", diagnostic.SeverityError,
			fmt.Sprintf("symbol %q already declared in this scope", sym.Name), sym.Span,
		)
		d.Related = []diagnostic.RelatedLocation{{Span: existing.Span, Message: "previous declaration"}}
		b.table.Diagnostics = append(b.table.Diagnostics, d)
		return id
	}
	id := b.appendSymbol(sym)
	if IsTestEntryPoint(sym) {
		return id
	}
	sc.Names[sym.Name] = id
	return id
}

func isForwardPair(a, b Kind) bool {
	return (a == KindForward && b.IsCallable() && b != KindForward) ||
		(b == KindForward && a.IsCallable() && a != KindForward)
}

func (b *builder) resolveFileReferences(fileScope ID) {
	file := b.table.Scopes[fileScope-1]
	for i := range b.table.References {
		if b.pollCancellation() {
			return
		}
		ref := &b.table.References[i]
		if ref.Resolved == 0 {
			if ref.IsCall {
				if resolved, ok := b.table.LookupCallable(ref.Scope, ref.Name, ref.ArgCount); ok {
					ref.Resolved = resolved.ID
				} else if resolved, ok := b.table.Lookup(ref.Scope, ref.Name); ok {
					ref.Resolved = resolved.ID
				}
			} else {
				ref.Resolved = file.Names[ref.Name]
			}
		}
	}
}

func (b *builder) reference(scope ID, name string, span source.Span, isCall bool, argCount int) {
	ref := Reference{Name: name, Span: span, Scope: scope, IsCall: isCall, ArgCount: argCount}
	var sym Symbol
	var ok bool
	if isCall {
		sym, ok = b.table.LookupCallable(scope, name, argCount)
		if !ok {
			sym, ok = b.table.Lookup(scope, name)
		}
	} else {
		sym, ok = b.table.Lookup(scope, name)
	}
	if ok {
		ref.Resolved = sym.ID
	}
	b.table.References = append(b.table.References, ref)
}

func (b *builder) spanOf(n parser.SyntaxNode) source.Span {
	if b.mapSpan != nil {
		if span, ok := b.mapSpan(n); ok {
			return span
		}
	}
	if origin, ok := n.Token().Origin(); ok && b.files != nil {
		span := origin.Span()
		file := b.files(span.File)
		if file.IsValid() {
			return source.Span{
				File: file, Start: source.Offset(span.Start.Offset), End: source.Offset(span.End.Offset),
			}
		}
	}
	return n.Range().Span(b.file)
}

func extractTag(n parser.SyntaxNode) string {
	tagNode, ok := n.Field("tag")
	if !ok {
		return ""
	}
	var tags []string
	it := tagNode.Children()
	for it.Next() {
		if text := it.Node().Text(); text != "" {
			tags = append(tags, text)
		}
	}
	return strings.Join(tags, "|")
}

func (b *builder) walk(scope ID, n parser.SyntaxNode) {
	if !n.Valid() || b.cancelled != nil || b.pollCancellation() {
		return
	}
	switch n.Kind() {
	case parser.KindBlock:
		child := b.newScope(ScopeBlock, scope)
		it := n.Children()
		for it.Next() {
			b.walk(child, it.Node())
		}
	case parser.KindVariableDeclaration:
		b.walkVariableDecl(scope, n)
	case parser.KindFunctionDefinition, parser.KindFunctionDeclaration:
		b.walkFunction(scope, n)
	case parser.KindEnumDeclaration:
		b.walkEnum(scope, n)
	case parser.KindForStatement:
		b.walkFor(scope, n)
	case parser.KindDefinedExpression:
		// defined(NAME) tests preprocessor macro state, a different
		// namespace entirely; its operand is not a symbol reference.
	case parser.KindTaggedExpression:
		if expression, ok := n.Field("expression"); ok {
			b.walk(scope, expression)
		}
	case parser.KindStateStatement, parser.KindStateSelector:
		// Automaton and state names use their own namespace.
	case parser.KindCallExpression:
		b.walkCall(scope, n)
	case parser.KindIdentifier:
		b.reference(scope, n.Text(), b.spanOf(n), false, -1)
	default:
		it := n.Children()
		for it.Next() {
			b.walk(scope, it.Node())
		}
	}
}

func (b *builder) walkCall(scope ID, n parser.SyntaxNode) {
	argCount := 0
	args, hasArgs := n.Field("arguments")
	if hasArgs {
		it := args.Children()
		for it.Next() {
			argCount++
		}
	}
	callee, ok := n.Field("function")
	if ok && callee.Kind() == parser.KindIdentifier {
		b.reference(scope, callee.Text(), b.spanOf(callee), true, argCount)
	} else if ok {
		b.walk(scope, callee)
	}
	if hasArgs {
		b.walk(scope, args)
	}
}

func (b *builder) walkVariableDecl(scope ID, n parser.SyntaxNode) {
	isConst, isStatic := false, false
	quals := n.Children()
	for quals.Next() {
		c := quals.Node()
		if c.Kind() != parser.KindIdentifier {
			continue
		}
		switch c.Token().Kind() {
		case token.KwConst:
			isConst = true
		case token.KwStatic:
			isStatic = true
		}
	}

	it := n.Children()
	for it.Next() {
		if b.pollCancellation() {
			return
		}
		d := it.Node()
		if d.Kind() != parser.KindVariableDeclarator {
			continue
		}
		nameNode, ok := d.Field("name")
		if !ok {
			continue
		}
		_, isArray := d.Field("array")
		sym := Symbol{
			Name: nameNode.Text(), Kind: KindVariable, Tag: extractTag(d),
			IsArray: isArray, IsConst: isConst, IsStatic: isStatic, Span: b.spanOf(nameNode),
		}
		b.declare(scope, sym)
		if init, ok := d.Field("initializer"); ok && b.keepDetails(b.spanOf(d)) {
			b.walk(scope, init)
		}
	}
}

func classifyFunctionKind(n parser.SyntaxNode) (kind Kind, isStatic bool) {
	hasNative, hasForward, hasPublic, hasStock := false, false, false, false
	it := n.Children()
	for it.Next() {
		c := it.Node()
		if c.Kind() != parser.KindIdentifier {
			continue
		}
		switch c.Token().Kind() {
		case token.KwNative:
			hasNative = true
		case token.KwForward:
			hasForward = true
		case token.KwPublic:
			hasPublic = true
		case token.KwStock:
			hasStock = true
		case token.KwStatic:
			isStatic = true
		}
	}
	switch {
	case hasNative:
		return KindNative, isStatic
	case hasForward:
		return KindForward, isStatic
	case hasPublic:
		return KindPublic, isStatic
	case hasStock:
		return KindStock, isStatic
	default:
		return KindFunction, isStatic
	}
}

func (b *builder) walkFunction(scope ID, n parser.SyntaxNode) {
	nameNode, ok := n.Field("name")
	if !ok {
		return
	}
	kind, isStatic := classifyFunctionKind(n)
	name := normalizeFunctionName(nameNode.Text())
	if strings.HasPrefix(name, "operator") {
		kind = KindOperator
	}
	minArgs, maxArgs, paramTags := functionSignature(n)
	sym := Symbol{
		Name: name, Kind: kind, Tag: extractTag(n), IsStatic: isStatic,
		MinArgs: minArgs, MaxArgs: maxArgs, ParamTags: paramTags, Span: b.spanOf(nameNode),
	}
	if selector, ok := n.Field("state"); ok {
		sym.StateSelector = selector.Text()
	}
	var id ID
	if kind == KindOperator {
		sym.Scope = scope
		id = b.appendSymbol(sym)
	} else {
		id = b.declare(scope, sym)
	}
	if !b.keepDetails(sym.Span) {
		return
	}

	funcScope := b.newScope(ScopeFunction, scope)
	b.table.Symbols[id-1].FuncScope = funcScope

	if params, ok := n.Field("parameters"); ok {
		it := params.Children()
		for it.Next() {
			if b.pollCancellation() {
				return
			}
			b.walkParameter(funcScope, it.Node())
		}
	}

	if body, ok := n.Field("body"); ok {
		b.walk(funcScope, body)
	}
}

func normalizeFunctionName(name string) string {
	if !strings.HasPrefix(name, "operator") {
		return name
	}
	return "operator" + strings.Join(strings.Fields(strings.TrimPrefix(name, "operator")), "")
}

func functionSignature(n parser.SyntaxNode) (minArgs, maxArgs int, tags []string) {
	params, ok := n.Field("parameters")
	if !ok {
		return 0, 0, nil
	}
	it := params.Children()
	for it.Next() {
		param := it.Node()
		if param.Kind() != parser.KindParameter {
			continue
		}
		name, _ := param.Field("name")
		_, generic := param.Field("generic")
		if strings.Contains(param.Text(), "...") || generic && name.Text() == "va_args" {
			return minArgs, -1, tags
		}
		maxArgs++
		tags = append(tags, extractTag(param))
		if _, optional := param.Field("default_value"); !optional {
			minArgs++
		}
	}
	return minArgs, maxArgs, tags
}

func (b *builder) walkParameter(scope ID, p parser.SyntaxNode) {
	if p.Kind() != parser.KindParameter {
		return
	}
	nameNode, ok := p.Field("name")
	if !ok {
		return
	}
	_, isArray := p.Field("array")
	isConst := false
	children := p.Children()
	for children.Next() {
		child := children.Node()
		if child.Kind() == parser.KindIdentifier && child.Token().Kind() == token.KwConst {
			isConst = true
			break
		}
	}
	sym := Symbol{
		Name: nameNode.Text(), Kind: KindParameter, Tag: extractTag(p),
		IsArray: isArray, IsConst: isConst, Span: b.spanOf(nameNode),
	}
	b.declare(scope, sym)
	if def, ok := p.Field("default_value"); ok {
		b.walk(scope, def)
	}
}

func (b *builder) walkEnum(scope ID, n parser.SyntaxNode) {
	enumTag := extractTag(n)
	if nameNode, ok := n.Field("name"); ok {
		b.declare(scope, Symbol{Name: nameNode.Text(), Kind: KindEnum, Span: b.spanOf(nameNode)})
	}
	body, ok := n.Field("body")
	if !ok {
		return
	}
	it := body.Children()
	for it.Next() {
		if b.pollCancellation() {
			return
		}
		entry := it.Node()
		if entry.Kind() != parser.KindEnumEntry {
			continue
		}
		nameNode, ok := entry.Field("name")
		if !ok {
			continue
		}
		tag := extractTag(entry)
		if tag == "" {
			tag = enumTag
		}
		_, isArray := entry.Field("array")
		b.declare(scope, Symbol{
			Name: nameNode.Text(), Kind: KindConstant, Tag: tag, IsArray: isArray, IsConst: true,
			Span: b.spanOf(nameNode),
		})
		if val, ok := entry.Field("value"); ok && b.keepDetails(b.spanOf(entry)) {
			b.walk(scope, val)
		}
	}
}

func (b *builder) walkFor(scope ID, n parser.SyntaxNode) {
	forScope := b.newScope(ScopeBlock, scope)
	if init, ok := n.Field("init"); ok {
		b.walk(forScope, init)
	}
	if cond, ok := n.Field("condition"); ok {
		b.walk(forScope, cond)
	}
	if inc, ok := n.Field("increment"); ok {
		b.walk(forScope, inc)
	}
	if body, ok := n.Field("body"); ok {
		b.walk(forScope, body)
	}
}
