package sema

import (
	"context"
	"fmt"
	"sort"
	"strings"

	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawn-parser/token"
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

// CheckStates validates automaton and state names used by state statements.
func CheckStates(root parser.SyntaxNode, file source.FileID) []diagnostic.Diagnostic {
	diagnostics, _ := checkStates(context.Background(), false, root, file)
	return diagnostics
}

// CheckStatesContext validates states and stops when ctx is cancelled.
func CheckStatesContext(
	ctx context.Context,
	root parser.SyntaxNode,
	file source.FileID,
) ([]diagnostic.Diagnostic, error) {
	return checkStates(ctx, true, root, file)
}

func checkStates(
	ctx context.Context,
	cancellable bool,
	root parser.SyntaxNode,
	file source.FileID,
) ([]diagnostic.Diagnostic, error) {
	cancel := cancellation{ctx: ctx, cancellable: cancellable}
	if cancellable {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if !root.Valid() {
		return nil, nil
	}
	automatons := make(map[string]map[string]struct{})
	implementations := make(map[string]stateImplementation)
	fallbacks := make(map[string]source.Span)
	var diagnostics []diagnostic.Diagnostic
	walkSyntaxContext(root, &cancel, func(node parser.SyntaxNode) {
		if node.Kind() != parser.KindFunctionDefinition && node.Kind() != parser.KindFunctionDeclaration {
			return
		}
		selector, ok := node.Field("state")
		if !ok {
			return
		}
		automaton, states := parseStateSelector(selector.Text())
		name, hasName := node.Field("name")
		if !hasName {
			return
		}
		if hasToken(node, token.KwForward) {
			diagnostics = append(diagnostics, diagnostic.New(
				"pawn-analysis:sema/forward-state-ignored", "pawn-analysis", diagnostic.SeverityWarning,
				"state selector on forward declaration is ignored", name.Range().Span(file),
			))
			return
		}
		if invalidStateFunction(node, name.Text()) {
			diagnostics = append(diagnostics, diagnostic.New(
				"pawn-analysis:sema/invalid-state-function", "pawn-analysis", diagnostic.SeverityError,
				"native and operator functions may not have states", name.Range().Span(file),
			))
		}
		if len(states) == 0 {
			fallbacks[name.Text()] = name.Range().Span(file)
			return
		}
		if automatons[automaton] == nil {
			automatons[automaton] = make(map[string]struct{})
		}
		for _, state := range states {
			automatons[automaton][state] = struct{}{}
		}
		previous, exists := implementations[name.Text()]
		if exists && previous.automaton != automaton {
			diagnostics = append(diagnostics, diagnostic.New(
				"pawn-analysis:sema/automaton-conflict", "pawn-analysis", diagnostic.SeverityError,
				fmt.Sprintf("%q may only belong to one automaton", name.Text()), name.Range().Span(file),
			))
			return
		}
		if !exists {
			previous = stateImplementation{
				automaton: automaton, states: make(map[string]struct{}), span: name.Range().Span(file),
			}
		}
		for _, state := range states {
			if _, conflict := previous.states[state]; conflict {
				diagnostics = append(diagnostics, diagnostic.New(
					"pawn-analysis:sema/state-conflict", "pawn-analysis", diagnostic.SeverityError,
					fmt.Sprintf("state %q is already assigned to another implementation of %q", state, name.Text()),
					name.Range().Span(file),
				))
			}
			previous.states[state] = struct{}{}
		}
		implementations[name.Text()] = previous
	})
	if cancel.err != nil {
		return nil, cancel.err
	}
	fallbackNames := make([]string, 0, len(fallbacks))
	for name := range fallbacks {
		fallbackNames = append(fallbackNames, name)
	}
	sort.Strings(fallbackNames)
	for _, name := range fallbackNames {
		span := fallbacks[name]
		if _, defined := implementations[name]; defined {
			continue
		}
		diagnostics = append(diagnostics, diagnostic.New(
			"pawn-analysis:sema/no-defined-states", "pawn-analysis", diagnostic.SeverityError,
			fmt.Sprintf("no states are defined for %q", name), span,
		))
	}
	stateVariables, err := checkStateVariables(root, file, &cancel)
	if err != nil {
		return nil, err
	}
	diagnostics = append(diagnostics, stateVariables...)

	used := make(map[string]bool)
	walkSyntaxContext(root, &cancel, func(node parser.SyntaxNode) {
		switch node.Kind() {
		case parser.KindCallExpression:
			if function, ok := node.Field("function"); ok && function.Kind() == parser.KindIdentifier {
				used[function.Text()] = true
			}
		case parser.KindFunctionDefinition, parser.KindFunctionDeclaration:
			if name, ok := node.Field("name"); ok && isPublicFunction(node) {
				used[name.Text()] = true
			}
		}
	})
	if cancel.err != nil {
		return nil, cancel.err
	}
	implementationNames := make([]string, 0, len(implementations))
	for name := range implementations {
		implementationNames = append(implementationNames, name)
	}
	sort.Strings(implementationNames)
	for _, name := range implementationNames {
		implementation := implementations[name]
		_, hasFallback := fallbacks[name]
		if !used[name] || hasFallback {
			continue
		}
		states := make([]string, 0, len(automatons[implementation.automaton]))
		for state := range automatons[implementation.automaton] {
			states = append(states, state)
		}
		sort.Strings(states)
		for _, state := range states {
			if _, covered := implementation.states[state]; covered {
				continue
			}
			diagnostics = append(diagnostics, diagnostic.New(
				"pawn-analysis:sema/missing-state-implementation", "pawn-analysis", diagnostic.SeverityWarning,
				fmt.Sprintf("no implementation for state %q in function %q and no fallback", state, name),
				implementation.span,
			))
		}
	}

	walkSyntaxContext(root, &cancel, func(node parser.SyntaxNode) {
		if node.Kind() != parser.KindStateStatement {
			return
		}
		first, ok := node.Field("state")
		if !ok {
			return
		}
		automaton, state, location := "", first.Text(), first
		if target, named := node.Field("target"); named {
			automaton, state, location = first.Text(), target.Text(), first
		}
		states, knownAutomaton := automatons[automaton]
		if !knownAutomaton {
			name := automaton
			if name == "" {
				name = "<main>"
			}
			diagnostics = append(diagnostics, diagnostic.New(
				"pawn-analysis:sema/unknown-automaton", "pawn-analysis", diagnostic.SeverityError,
				fmt.Sprintf("unknown automaton %q", name), location.Range().Span(file),
			))
			return
		}
		if _, known := states[state]; !known {
			name := automaton
			if name == "" {
				name = "<main>"
			}
			diagnostics = append(diagnostics, diagnostic.New(
				"pawn-analysis:sema/unknown-state", "pawn-analysis", diagnostic.SeverityError,
				fmt.Sprintf("unknown state %q for automaton %q", state, name), location.Range().Span(file),
			))
		}
	})
	if cancel.err != nil {
		return nil, cancel.err
	}
	return diagnostics, nil
}

type stateImplementation struct {
	automaton string
	states    map[string]struct{}
	span      source.Span
}

func isPublicFunction(node parser.SyntaxNode) bool {
	it := node.Children()
	for it.Next() {
		child := it.Node()
		if child.Kind() == parser.KindIdentifier && child.Token().Kind() == token.KwPublic {
			return true
		}
	}
	return false
}

func invalidStateFunction(node parser.SyntaxNode, name string) bool {
	if strings.HasPrefix(strings.TrimSpace(name), "operator") {
		return true
	}
	it := node.Children()
	for it.Next() {
		child := it.Node()
		if child.Kind() == parser.KindIdentifier && child.Token().Kind() == token.KwNative {
			return true
		}
	}
	return false
}

func checkStateVariables(
	root parser.SyntaxNode,
	file source.FileID,
	cancel *cancellation,
) ([]diagnostic.Diagnostic, error) {
	var diagnostics []diagnostic.Diagnostic
	ordinaryGlobals := make(map[string]struct{})
	stateGlobals := make(map[string]struct{})
	decls := root.Declarations()
	for decls.Next() {
		if cancel.poll() {
			return nil, cancel.err
		}
		declaration := decls.Declaration()
		if declaration.Kind() != parser.KindVariableDeclaration {
			continue
		}
		isPublic := hasToken(declaration, token.KwPublic)
		it := declaration.Children()
		for it.Next() {
			variable := it.Node()
			if variable.Kind() != parser.KindVariableDeclarator {
				continue
			}
			name, hasName := variable.Field("name")
			if !hasName {
				continue
			}
			_, stateful := variable.Field("capacity")
			if !stateful {
				ordinaryGlobals[name.Text()] = struct{}{}
				continue
			}
			if variableTag(variable) == "Iterator" {
				continue
			}
			selector, _ := variable.Field("capacity")
			_, states := parseStateSelector(selector.Text())
			if len(states) == 0 {
				diagnostics = append(diagnostics, diagnostic.New(
					"pawn-analysis:sema/no-defined-states", "pawn-analysis", diagnostic.SeverityError,
					fmt.Sprintf("no states are defined for %q", name.Text()), name.Range().Span(file),
				))
			}
			if _, seen := stateGlobals[name.Text()]; seen {
				if _, initialized := variable.Field("initializer"); initialized {
					diagnostics = append(diagnostics, diagnostic.New(
						"pawn-analysis:sema/initialized-state-variable", "pawn-analysis", diagnostic.SeverityError,
						fmt.Sprintf("state variable %q may not be initialized", name.Text()), name.Range().Span(file),
					))
				}
			}
			stateGlobals[name.Text()] = struct{}{}
			if isPublic {
				diagnostics = append(diagnostics, stateVariableDiagnostic(name, file))
			}
			if _, shadows := ordinaryGlobals[name.Text()]; shadows {
				diagnostics = append(diagnostics, diagnostic.New(
					"pawn-analysis:sema/state-variable-shadow", "pawn-analysis", diagnostic.SeverityWarning,
					fmt.Sprintf("state variable %q shadows a global variable", name.Text()), name.Range().Span(file),
				))
			}
		}
	}
	walkSyntaxContext(root, cancel, func(node parser.SyntaxNode) {
		if node.Kind() != parser.KindFunctionDefinition {
			return
		}
		body, ok := node.Field("body")
		if !ok {
			return
		}
		walkSyntaxContext(body, cancel, func(child parser.SyntaxNode) {
			if child.Kind() != parser.KindVariableDeclarator {
				return
			}
			if _, stateful := child.Field("capacity"); !stateful {
				return
			}
			if variableTag(child) == "Iterator" {
				return
			}
			if name, ok := child.Field("name"); ok {
				diagnostics = append(diagnostics, stateVariableDiagnostic(name, file))
			}
		})
	})
	if cancel.err != nil {
		return nil, cancel.err
	}
	return diagnostics, nil
}

func variableTag(node parser.SyntaxNode) string {
	tag, ok := node.Field("tag")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(tag.Text(), ":")
}

func stateVariableDiagnostic(name parser.SyntaxNode, file source.FileID) diagnostic.Diagnostic {
	return diagnostic.New(
		"pawn-analysis:sema/invalid-state-variable", "pawn-analysis", diagnostic.SeverityError,
		"public and local variables may not have states", name.Range().Span(file),
	)
}

func hasToken(node parser.SyntaxNode, kind token.Kind) bool {
	it := node.Children()
	for it.Next() {
		if it.Node().Token().Kind() == kind {
			return true
		}
	}
	return false
}

func parseStateSelector(text string) (string, []string) {
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(text), "<"), ">"))
	if text == "" {
		return "", nil
	}
	automaton := ""
	if before, after, ok := strings.Cut(text, ":"); ok {
		automaton, text = strings.TrimSpace(before), after
	}
	var states []string
	for _, state := range strings.Split(text, ",") {
		if state = strings.TrimSpace(state); state != "" {
			states = append(states, state)
		}
	}
	return automaton, states
}

func walkSyntaxContext(node parser.SyntaxNode, cancel *cancellation, visit func(parser.SyntaxNode)) {
	if !node.Valid() || cancel != nil && (cancel.err != nil || cancel.poll()) {
		return
	}
	visit(node)
	it := node.Children()
	for it.Next() {
		walkSyntaxContext(it.Node(), cancel, visit)
	}
}
