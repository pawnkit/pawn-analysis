package sema

import (
	"context"
	"crypto/sha256"

	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

// NameCache stores immutable per-function name checks between revisions.
type NameCache struct {
	exports  [32]byte
	revision [32]byte
	entries  map[symbol.StableID]cachedNames
}

type cachedNames struct {
	body   [32]byte
	start  source.Offset
	result Result
}

const nameCacheMinimumBytes = 128

// CheckNamesCachedIndexedContext reuses checks for unchanged function bodies.
func CheckNamesCachedIndexedContext(
	ctx context.Context,
	root parser.SyntaxNode,
	table *symbol.Table,
	resolver Resolver,
	previous *NameCache,
	revision string,
	declarations parser.DeclarationIndex,
) (Result, *NameCache, int, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, nil, 0, err
	}
	if !root.Valid() || table == nil {
		return Result{}, &NameCache{entries: make(map[symbol.StableID]cachedNames)}, 0, nil
	}

	cacheable := nameFunctions(root, table, declarations)
	exports := table.ExportFingerprint()
	revisionHash := sha256.Sum256([]byte(revision))
	next := &NameCache{
		exports: exports, revision: revisionHash,
		entries: make(map[symbol.StableID]cachedNames, len(cacheable)),
	}
	canReuse := previous != nil && previous.exports == exports && previous.revision == revisionHash
	owners := functionOwners(table)
	refsByFunction := make(map[symbol.StableID][]symbol.Reference)
	for _, ref := range table.References {
		if owner, ok := owners[ref.Scope]; ok {
			refsByFunction[owner] = append(refsByFunction[owner], ref)
		}
	}

	var result Result
	seen := make(map[symbol.StableID]bool)
	reused := 0
	for _, ref := range table.References {
		owner, ok := owners[ref.Scope]
		if !ok {
			part, err := checkNameReferences(ctx, true, table, resolver, []symbol.Reference{ref})
			if err != nil {
				return Result{}, nil, 0, err
			}
			appendNameResult(&result, part)
			continue
		}
		if seen[owner] {
			continue
		}
		seen[owner] = true
		entry, cacheableFunction := cacheable[owner]
		if canReuse && cacheableFunction {
			if cached, found := previous.entries[owner]; found && cached.body == entry.body {
				part := shiftNameResult(cached.result, table.File, entry.start-cached.start)
				appendNameResult(&result, part)
				next.entries[owner] = cachedNames{body: entry.body, start: entry.start, result: part}
				reused++
				continue
			}
		}
		part, err := checkNameReferences(ctx, true, table, resolver, refsByFunction[owner])
		if err != nil {
			return Result{}, nil, 0, err
		}
		appendNameResult(&result, part)
		if cacheableFunction {
			next.entries[owner] = cachedNames{body: entry.body, start: entry.start, result: cloneNameResult(part)}
		}
	}
	return result, next, reused, nil
}

type nameFunction struct {
	body  [32]byte
	start source.Offset
}

func nameFunctions(
	root parser.SyntaxNode,
	table *symbol.Table,
	declarations parser.DeclarationIndex,
) map[symbol.StableID]nameFunction {
	result := make(map[symbol.StableID]nameFunction)
	nodes := root.Declarations()
	position := 0
	for nodes.Next() {
		declaration := nodes.Declaration()
		boundary, indexed := declarations.At(position)
		position++
		if declaration.Kind() != parser.KindFunctionDefinition || len(declaration.Bytes()) < nameCacheMinimumBytes {
			continue
		}
		name, ok := declaration.Field("name")
		if !ok {
			continue
		}
		item, ok := declaredSymbol(table, name)
		if !ok || item.FuncScope == 0 {
			continue
		}
		stable, ok := table.StableSymbolID(item.ID)
		if !ok {
			continue
		}
		body := sha256.Sum256(declaration.Bytes())
		if indexed && boundary.Kind == declaration.Kind() && boundary.Range == declaration.Range() {
			body = boundary.Fingerprint
		}
		result[stable] = nameFunction{body: body, start: source.Offset(declaration.Range().Start)}
	}
	return result
}

func functionOwners(table *symbol.Table) map[symbol.ID]symbol.StableID {
	functions := make(map[symbol.ID]symbol.StableID)
	for _, item := range table.Symbols {
		if !item.Kind.IsCallable() || item.FuncScope == 0 {
			continue
		}
		stable, ok := table.StableSymbolID(item.ID)
		if ok {
			functions[item.FuncScope] = stable
		}
	}

	owners := make(map[symbol.ID]symbol.StableID)
	for _, ref := range table.References {
		for scope := ref.Scope; scope != 0; {
			if owner, ok := functions[scope]; ok {
				owners[ref.Scope] = owner
				break
			}
			current, ok := table.Scope(scope)
			if !ok {
				break
			}
			scope = current.Parent
		}
	}
	return owners
}

func appendNameResult(dst *Result, src Result) {
	dst.Diagnostics = append(dst.Diagnostics, src.Diagnostics...)
	dst.Unknown = append(dst.Unknown, src.Unknown...)
}

func cloneNameResult(result Result) Result {
	return Result{
		Diagnostics: append([]diagnostic.Diagnostic(nil), result.Diagnostics...),
		Unknown:     append([]symbol.Reference(nil), result.Unknown...),
	}
}

func shiftNameResult(result Result, file source.FileID, delta source.Offset) Result {
	shifted := cloneNameResult(result)
	for i := range shifted.Diagnostics {
		shifted.Diagnostics[i].Primary.File = file
		shifted.Diagnostics[i].Primary.Start += delta
		shifted.Diagnostics[i].Primary.End += delta
		shifted.Diagnostics[i].Related = append([]diagnostic.RelatedLocation(nil), shifted.Diagnostics[i].Related...)
		for j := range shifted.Diagnostics[i].Related {
			shifted.Diagnostics[i].Related[j].Span.File = file
			shifted.Diagnostics[i].Related[j].Span.Start += delta
			shifted.Diagnostics[i].Related[j].Span.End += delta
		}
	}
	for i := range shifted.Unknown {
		shifted.Unknown[i].Span.File = file
		shifted.Unknown[i].Span.Start += delta
		shifted.Unknown[i].Span.End += delta
	}
	return shifted
}
