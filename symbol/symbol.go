// Package symbol builds per-file Pawn symbol tables.
package symbol

import (
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

// Kind classifies a declared symbol.
type Kind uint8

const (
	KindFunction Kind = iota + 1
	KindPublic
	KindNative
	KindForward
	KindStock
	KindVariable
	KindConstant
	KindEnum
	KindParameter
	KindOperator
)

func (k Kind) String() string {
	switch k {
	case KindFunction:
		return "function"
	case KindPublic:
		return "public"
	case KindNative:
		return "native"
	case KindForward:
		return "forward"
	case KindStock:
		return "stock"
	case KindVariable:
		return "variable"
	case KindConstant:
		return "constant"
	case KindEnum:
		return "enum"
	case KindParameter:
		return "parameter"
	case KindOperator:
		return "operator"
	default:
		return "unknown"
	}
}

// IsCallable reports whether k denotes something invoked with call syntax.
func (k Kind) IsCallable() bool {
	switch k {
	case KindFunction, KindPublic, KindNative, KindForward, KindStock, KindOperator:
		return true
	default:
		return false
	}
}

// OperatorOverloads returns operator declarations with the given normalized name.
func (t *Table) OperatorOverloads(name string) []Symbol {
	var result []Symbol
	for _, item := range t.Symbols {
		if item.Kind == KindOperator && item.Name == name {
			result = append(result, item)
		}
	}
	return result
}

// ID identifies a [Symbol] or [Scope] within one [Table]. The zero value
// never refers to a real entry.
type ID int

// Symbol is one declaration.
type Symbol struct {
	ID            ID
	Name          string
	Kind          Kind
	Tag           string // declared tag, e.g. "Float"; "" if untagged.
	IsArray       bool
	IsConst       bool
	IsStatic      bool
	Scope         ID          // the scope this symbol is declared IN.
	FuncScope     ID          // for callable kinds with a body: the scope holding its parameters/locals; 0 otherwise.
	MinArgs       int         // required parameters for callable symbols.
	MaxArgs       int         // total parameters; -1 means variadic.
	ParamTags     []string    // parameter tags in declaration order.
	StateSelector string      // state selector text; empty for ordinary declarations.
	Span          source.Span // the declaration name's span.
}

// ScopeKind classifies a [Scope].
type ScopeKind uint8

const (
	ScopeFile ScopeKind = iota + 1
	ScopeFunction
	ScopeBlock
)

// Scope is one lexical scope. Names declared directly in a scope shadow
// same-named symbols in Parent.
type Scope struct {
	ID     ID
	Kind   ScopeKind
	Parent ID // 0 for the file scope.
	Names  map[string]ID
}

// Reference is one identifier use, resolved against the scope active at
// that point in the tree.
type Reference struct {
	Name     string
	Span     source.Span
	Scope    ID
	Resolved ID // 0 if no declaration was found.
	IsCall   bool
	ArgCount int // -1 for non-call references.
}

// Table is the immutable result of [Build].
type Table struct {
	File        source.FileID
	Symbols     []Symbol
	Scopes      []Scope
	References  []Reference
	Diagnostics []diagnostic.Diagnostic

	declarations map[source.Span]ID
	references   map[source.Span]ID
}

// Symbol looks up a symbol by ID.
func (t *Table) Symbol(id ID) (Symbol, bool) {
	if id <= 0 || int(id) > len(t.Symbols) {
		return Symbol{}, false
	}
	return t.Symbols[id-1], true
}

// Scope looks up a scope by ID.
func (t *Table) Scope(id ID) (Scope, bool) {
	if id <= 0 || int(id) > len(t.Scopes) {
		return Scope{}, false
	}
	return t.Scopes[id-1], true
}

// DeclarationAt returns the first symbol declared at span.
func (t *Table) DeclarationAt(span source.Span) (Symbol, bool) {
	if id := t.declarations[span]; id != 0 {
		return t.Symbol(id)
	}
	return Symbol{}, false
}

// ReferencedAt returns the symbol resolved by the first reference at span.
func (t *Table) ReferencedAt(span source.Span) (Symbol, bool) {
	if id := t.references[span]; id != 0 {
		return t.Symbol(id)
	}
	return Symbol{}, false
}

// Lookup resolves name starting at scope and walking outward through
// parent scopes, the same rule used while building references.
func (t *Table) Lookup(scope ID, name string) (Symbol, bool) {
	for scope != 0 {
		sc, ok := t.Scope(scope)
		if !ok {
			return Symbol{}, false
		}
		if id, ok := sc.Names[name]; ok {
			return t.Symbol(id)
		}
		scope = sc.Parent
	}
	return Symbol{}, false
}

// UndefinedReferences returns names unresolved within this file.
func (t *Table) UndefinedReferences() []Reference {
	var out []Reference
	for _, r := range t.References {
		if r.Resolved == 0 {
			out = append(out, r)
		}
	}
	return out
}
