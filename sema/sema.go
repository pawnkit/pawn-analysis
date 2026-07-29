// Package sema provides semantic checks over shared symbol tables.
package sema

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/pawnkit/pawn-analysis/symbol"
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

// NameState is an external resolver's answer for a name.
type NameState uint8

const (
	NameUnknown NameState = iota
	NameFound
	NameMissing
)

// Resolver checks names supplied by includes, host APIs, or dependencies.
type Resolver interface {
	ResolveName(name string) NameState
}

// Callable describes an externally supplied function signature.
type Callable struct {
	ReturnTag string
	ParamTags []string
	MinArgs   int
	MaxArgs   int
}

// CallableResolver optionally supplies signatures for resolved names.
type CallableResolver interface {
	ResolveCallable(name string) (Callable, bool)
}

// Result contains confirmed diagnostics and unresolved unknowns.
type Result struct {
	Diagnostics []diagnostic.Diagnostic
	Unknown     []symbol.Reference
}

// NameCache stores immutable function results between revisions.
type NameCache struct {
	exports  [32]byte
	revision [32]byte
	entries  map[symbol.StableID]cachedNames
}

type cachedNames struct {
	references     [32]byte
	start          source.Offset
	diagnostics    []diagnostic.Diagnostic
	unknownIndexes []int
}

// CheckNames classifies references unresolved by the local symbol table.
func CheckNames(table *symbol.Table, resolver Resolver) Result {
	result, _ := checkNames(context.Background(), false, table, resolver)
	return result
}

// CheckNamesContext classifies names and stops when ctx is cancelled.
func CheckNamesContext(ctx context.Context, table *symbol.Table, resolver Resolver) (Result, error) {
	return checkNames(ctx, true, table, resolver)
}

// CheckNamesCachedContext reuses checks for unchanged functions.
func CheckNamesCachedContext(
	ctx context.Context,
	table *symbol.Table,
	resolver Resolver,
	previous *NameCache,
	revision string,
) (Result, *NameCache, int, error) {
	return checkNamesCached(ctx, true, table, resolver, previous, revision)
}

func checkNames(ctx context.Context, cancellable bool, table *symbol.Table, resolver Resolver) (Result, error) {
	var result Result
	if cancellable {
		if err := ctx.Err(); err != nil {
			return result, err
		}
	}
	if table == nil {
		return result, nil
	}
	cancel := cancellation{ctx: ctx, cancellable: cancellable}
	if err := checkNameReferences(&result, table, resolver, table.References, &cancel); err != nil {
		return Result{}, err
	}
	return result, nil
}

func checkNamesCached(
	ctx context.Context,
	cancellable bool,
	table *symbol.Table,
	resolver Resolver,
	previous *NameCache,
	revision string,
) (Result, *NameCache, int, error) {
	var result Result
	if cancellable {
		if err := ctx.Err(); err != nil {
			return result, nil, 0, err
		}
	}
	if table == nil {
		return result, &NameCache{entries: make(map[symbol.StableID]cachedNames)}, 0, nil
	}

	cancel := cancellation{ctx: ctx, cancellable: cancellable}
	exports := table.ExportFingerprint()
	revisionHash := sha256.Sum256([]byte(revision))
	next := &NameCache{
		exports: exports, revision: revisionHash,
		entries: make(map[symbol.StableID]cachedNames),
	}
	canReuse := previous != nil && previous.exports == exports && previous.revision == revisionHash
	owners := nameReferenceOwners(table)
	reused := 0
	for start := 0; start < len(table.References); {
		if cancel.poll() {
			return Result{}, nil, 0, cancel.err
		}
		owner := owners[start]
		owned := owner.id != (symbol.StableID{})
		end := start + 1
		if owned {
			for end < len(table.References) && owners[end].id == owner.id {
				end++
			}
		}
		references := table.References[start:end]
		if owned {
			fingerprint := nameReferenceFingerprint(table, references, owner.start)
			if canReuse {
				if cached, found := previous.entries[owner.id]; found && cached.references == fingerprint {
					delta := owner.start - cached.start
					diagnostics := shiftNameDiagnostics(cached.diagnostics, table.File, delta)
					unknown := referencesAt(references, cached.unknownIndexes)
					result.Diagnostics = append(result.Diagnostics, diagnostics...)
					result.Unknown = append(result.Unknown, unknown...)
					next.entries[owner.id] = cachedNames{
						references: fingerprint, start: owner.start,
						diagnostics: diagnostics, unknownIndexes: cached.unknownIndexes,
					}
					reused++
					start = end
					continue
				}
			}
			var checked Result
			if err := checkNameReferences(&checked, table, resolver, references, &cancel); err != nil {
				return Result{}, nil, 0, err
			}
			result.Diagnostics = append(result.Diagnostics, checked.Diagnostics...)
			result.Unknown = append(result.Unknown, checked.Unknown...)
			next.entries[owner.id] = cachedNames{
				references: fingerprint, start: owner.start,
				diagnostics:    append([]diagnostic.Diagnostic(nil), checked.Diagnostics...),
				unknownIndexes: unknownReferenceIndexes(references, checked.Unknown),
			}
			start = end
			continue
		}
		if err := checkNameReferences(&result, table, resolver, references, &cancel); err != nil {
			return Result{}, nil, 0, err
		}
		start = end
	}
	return result, next, reused, nil
}

func checkNameReferences(
	result *Result,
	table *symbol.Table,
	resolver Resolver,
	references []symbol.Reference,
	cancel *cancellation,
) error {
	for _, ref := range references {
		if cancel != nil && cancel.poll() {
			return cancel.err
		}
		if ref.Resolved != 0 {
			resolved, ok := table.Symbol(ref.Resolved)
			if ref.IsCall && ok && !resolved.Kind.IsCallable() {
				result.Diagnostics = append(result.Diagnostics, diagnostic.New(
					"pawn-analysis:sema/not-callable",
					"pawn-analysis",
					diagnostic.SeverityError,
					fmt.Sprintf("%q is not callable", ref.Name),
					ref.Span,
				))
			} else if ref.IsCall && ok && !acceptsArity(resolved, ref.ArgCount) {
				result.Diagnostics = append(result.Diagnostics, diagnostic.New(
					"pawn-analysis:sema/argument-count",
					"pawn-analysis",
					diagnostic.SeverityError,
					arityMessage(ref.Name, resolved.MinArgs, resolved.MaxArgs, ref.ArgCount),
					ref.Span,
				))
			}
			continue
		}

		state := NameUnknown
		if resolver != nil {
			state = resolver.ResolveName(ref.Name)
		}

		switch state {
		case NameFound:
			if ref.IsCall {
				checkExternalArity(result, resolver, ref)
			}
			continue
		case NameMissing:
			result.Diagnostics = append(result.Diagnostics, diagnostic.New(
				"pawn-analysis:sema/undefined-symbol",
				"pawn-analysis",
				diagnostic.SeverityError,
				fmt.Sprintf("undefined symbol %q", ref.Name),
				ref.Span,
			))
		default:
			result.Unknown = append(result.Unknown, ref)
		}
	}
	return nil
}

type nameReferenceOwner struct {
	id    symbol.StableID
	start source.Offset
}

func nameReferenceOwners(table *symbol.Table) []nameReferenceOwner {
	scopeOwners := make(map[symbol.ID]nameReferenceOwner)
	for _, item := range table.Symbols {
		if item.FuncScope == 0 {
			continue
		}
		stableID, ok := table.StableSymbolID(item.ID)
		if ok {
			scopeOwners[item.FuncScope] = nameReferenceOwner{id: stableID, start: item.Span.Start}
		}
	}
	owners := make([]nameReferenceOwner, len(table.References))
	for index, reference := range table.References {
		for scope := reference.Scope; scope != 0; {
			if owner, ok := scopeOwners[scope]; ok {
				owners[index] = owner
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

func nameReferenceFingerprint(
	table *symbol.Table,
	references []symbol.Reference,
	base source.Offset,
) [32]byte {
	hash := sha256.New()
	var encoded [8]byte
	for _, reference := range references {
		binary.LittleEndian.PutUint64(encoded[:], uint64(len(reference.Name)))
		hash.Write(encoded[:])
		hash.Write([]byte(reference.Name))
		if reference.IsCall {
			hash.Write([]byte{1})
		} else {
			hash.Write([]byte{0})
		}
		binary.LittleEndian.PutUint64(encoded[:], uint64(reference.ArgCount))
		hash.Write(encoded[:])
		binary.LittleEndian.PutUint64(encoded[:], uint64(reference.Span.Start-base))
		hash.Write(encoded[:])
		binary.LittleEndian.PutUint64(encoded[:], uint64(reference.Span.End-base))
		hash.Write(encoded[:])
		if resolved, ok := table.Symbol(reference.Resolved); ok {
			hash.Write([]byte{byte(resolved.Kind)})
			binary.LittleEndian.PutUint64(encoded[:], uint64(resolved.MinArgs))
			hash.Write(encoded[:])
			binary.LittleEndian.PutUint64(encoded[:], uint64(resolved.MaxArgs))
			hash.Write(encoded[:])
		}
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func shiftNameDiagnostics(items []diagnostic.Diagnostic, file source.FileID, delta source.Offset) []diagnostic.Diagnostic {
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

func unknownReferenceIndexes(references, unknown []symbol.Reference) []int {
	if len(unknown) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(unknown))
	position := 0
	for _, item := range unknown {
		for position < len(references) {
			if references[position].Name == item.Name && references[position].Span == item.Span {
				indexes = append(indexes, position)
				position++
				break
			}
			position++
		}
	}
	return indexes
}

func referencesAt(references []symbol.Reference, indexes []int) []symbol.Reference {
	result := make([]symbol.Reference, 0, len(indexes))
	for _, index := range indexes {
		if index >= 0 && index < len(references) {
			result = append(result, references[index])
		}
	}
	return result
}

func checkExternalArity(result *Result, resolver Resolver, ref symbol.Reference) {
	callables, ok := resolver.(CallableResolver)
	if !ok {
		return
	}
	callable, ok := callables.ResolveCallable(ref.Name)
	if !ok || (ref.ArgCount >= callable.MinArgs && (callable.MaxArgs < 0 || ref.ArgCount <= callable.MaxArgs)) {
		return
	}
	result.Diagnostics = append(result.Diagnostics, diagnostic.New(
		"pawn-analysis:sema/argument-count", "pawn-analysis", diagnostic.SeverityError,
		arityMessage(ref.Name, callable.MinArgs, callable.MaxArgs, ref.ArgCount), ref.Span,
	))
}

func acceptsArity(callable symbol.Symbol, count int) bool {
	return count >= callable.MinArgs && (callable.MaxArgs < 0 || count <= callable.MaxArgs)
}

func arityMessage(name string, minArgs, maxArgs, got int) string {
	switch {
	case maxArgs < 0:
		return fmt.Sprintf("%q expects at least %d arguments, got %d", name, minArgs, got)
	case minArgs == maxArgs:
		return fmt.Sprintf("%q expects %d arguments, got %d", name, minArgs, got)
	default:
		return fmt.Sprintf("%q expects %d to %d arguments, got %d", name, minArgs, maxArgs, got)
	}
}

// MapResolver resolves known names and treats all others as missing.
type MapResolver map[string]struct{}

func (m MapResolver) ResolveName(name string) NameState {
	if _, ok := m[name]; ok {
		return NameFound
	}
	return NameMissing
}
