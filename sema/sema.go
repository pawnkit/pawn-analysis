// Package sema provides semantic checks over shared symbol tables.
package sema

import (
	"fmt"

	"github.com/pawnkit/pawn-analysis/symbol"
	"github.com/pawnkit/pawnkit-core/diagnostic"
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

// CheckNames classifies references unresolved by the local symbol table.
func CheckNames(table *symbol.Table, resolver Resolver) Result {
	var result Result
	if table == nil {
		return result
	}

	for _, ref := range table.References {
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
				checkExternalArity(&result, resolver, ref)
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
