// Package cfg builds lightweight control-flow graphs for Pawn functions.
package cfg

import (
	"sort"

	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/source"
)

type ID int

type Block struct {
	ID         ID
	Kind       parser.Kind
	Span       source.Span
	Successors []ID
}

type Graph struct {
	Blocks          []Block
	Entry           ID
	FallsThrough    bool
	UnresolvedGotos []Goto
}

// Goto records a jump whose label was not found in the function.
type Goto struct {
	Block ID
	Name  string
	Span  source.Span
}

// ConstantEvaluator returns a constant expression's value when known.
type ConstantEvaluator func(parser.SyntaxNode) (int64, bool)

// Build creates a graph for a function body.
func Build(body parser.SyntaxNode, file source.FileID) *Graph {
	return BuildWithEvaluator(body, file, nil)
}

// BuildWithEvaluator prunes edges whose conditions have known constant values.
func BuildWithEvaluator(body parser.SyntaxNode, file source.FileID, eval ConstantEvaluator) *Graph {
	b := builder{
		graph: &Graph{}, file: file, eval: eval,
		labels: make(map[string]ID), gotos: make(map[string][]Goto),
	}
	part := b.sequence(body)
	b.graph.Entry = part.entry
	for _, jumps := range b.gotos {
		b.graph.UnresolvedGotos = append(b.graph.UnresolvedGotos, jumps...)
	}
	sort.Slice(b.graph.UnresolvedGotos, func(i, j int) bool {
		return b.graph.UnresolvedGotos[i].Block < b.graph.UnresolvedGotos[j].Block
	})
	reachable := b.graph.Reachable()
	for _, exit := range part.exits {
		if reachable[exit] {
			b.graph.FallsThrough = true
			break
		}
	}
	return b.graph
}

// Reachable returns blocks reachable from Entry.
func (g *Graph) Reachable() map[ID]bool {
	seen := make(map[ID]bool, len(g.Blocks))
	var visit func(ID)
	visit = func(id ID) {
		if id == 0 || seen[id] {
			return
		}
		seen[id] = true
		for _, next := range g.Blocks[id-1].Successors {
			visit(next)
		}
	}
	visit(g.Entry)
	return seen
}

type builder struct {
	graph  *Graph
	file   source.FileID
	eval   ConstantEvaluator
	labels map[string]ID
	gotos  map[string][]Goto
}

type fragment struct {
	entry     ID
	exits     []ID
	breaks    []ID
	continues []ID
}

func (b *builder) sequence(block parser.SyntaxNode) fragment {
	var result fragment
	it := block.Children()
	for it.Next() {
		part := b.statement(it.Node())
		if part.entry == 0 {
			continue
		}
		if result.entry == 0 {
			result = part
			continue
		}
		for _, exit := range result.exits {
			b.edge(exit, part.entry)
		}
		if len(result.exits) != 0 || it.Node().Kind() == parser.KindLabelStatement {
			result.exits = part.exits
		}
		result.breaks = append(result.breaks, part.breaks...)
		result.continues = append(result.continues, part.continues...)
	}
	return result
}

func (b *builder) statement(node parser.SyntaxNode) fragment {
	switch node.Kind() {
	case parser.KindBlock:
		return b.sequence(node)
	case parser.KindIfStatement:
		return b.branch(node)
	case parser.KindSwitchStatement:
		return b.switchStatement(node)
	case parser.KindWhileStatement, parser.KindDoWhileStatement, parser.KindForStatement:
		return b.loop(node)
	case parser.KindReturnStatement:
		id := b.block(node)
		return fragment{entry: id}
	case parser.KindBreakStatement:
		id := b.block(node)
		return fragment{entry: id, breaks: []ID{id}}
	case parser.KindContinueStatement:
		id := b.block(node)
		return fragment{entry: id, continues: []ID{id}}
	case parser.KindGotoStatement:
		return b.gotoStatement(node)
	case parser.KindLabelStatement:
		return b.labelStatement(node)
	default:
		id := b.block(node)
		return fragment{entry: id, exits: []ID{id}}
	}
}

func (b *builder) gotoStatement(node parser.SyntaxNode) fragment {
	id := b.block(node)
	label, ok := node.Field("label")
	if !ok || label.Text() == "" {
		return fragment{entry: id}
	}
	name := label.Text()
	if target := b.labels[name]; target != 0 {
		b.edge(id, target)
	} else {
		b.gotos[name] = append(b.gotos[name], Goto{Block: id, Name: name, Span: node.Range().Span(b.file)})
	}
	return fragment{entry: id}
}

func (b *builder) labelStatement(node parser.SyntaxNode) fragment {
	id := b.block(node)
	label, ok := node.Field("label")
	if !ok || label.Text() == "" {
		return fragment{entry: id, exits: []ID{id}}
	}
	name := label.Text()
	if b.labels[name] == 0 {
		b.labels[name] = id
		for _, jump := range b.gotos[name] {
			b.edge(jump.Block, id)
		}
		delete(b.gotos, name)
	}
	return fragment{entry: id, exits: []ID{id}}
}

func (b *builder) switchStatement(node parser.SyntaxNode) fragment {
	header := b.block(node)
	result := fragment{entry: header}
	hasDefault := false

	it := node.Children()
	for it.Next() {
		clause := it.Node()
		if clause.Kind() != parser.KindCaseClause && clause.Kind() != parser.KindDefaultClause {
			continue
		}
		if clause.Kind() == parser.KindDefaultClause {
			hasDefault = true
		}
		body, ok := clause.Field("body")
		if !ok {
			result.exits = append(result.exits, header)
			continue
		}
		part := b.statement(body)
		if part.entry == 0 {
			result.exits = append(result.exits, header)
			continue
		}
		b.edge(header, part.entry)
		result.exits = append(result.exits, part.exits...)
		result.exits = append(result.exits, part.breaks...)
		result.continues = append(result.continues, part.continues...)
	}
	if !hasDefault {
		result.exits = append(result.exits, header)
	}
	return result
}

func (b *builder) branch(node parser.SyntaxNode) fragment {
	header := b.block(node)
	var exits []ID
	var breaks []ID
	var continues []ID
	value, known := b.condition(node)
	if consequence, ok := node.Field("consequence"); ok {
		part := b.statement(consequence)
		if !known || value != 0 {
			b.edge(header, part.entry)
			exits = append(exits, part.exits...)
			breaks = append(breaks, part.breaks...)
			continues = append(continues, part.continues...)
		}
	} else {
		exits = append(exits, header)
	}
	if alternative, ok := node.Field("alternative"); ok {
		part := b.statement(alternative)
		if !known || value == 0 {
			b.edge(header, part.entry)
			exits = append(exits, part.exits...)
			breaks = append(breaks, part.breaks...)
			continues = append(continues, part.continues...)
		}
	} else if !known || value == 0 {
		exits = append(exits, header)
	}
	return fragment{entry: header, exits: exits, breaks: breaks, continues: continues}
}

func (b *builder) loop(node parser.SyntaxNode) fragment {
	header := b.block(node)
	value, known := b.condition(node)
	if node.Kind() == parser.KindForStatement {
		if _, ok := node.Field("condition"); !ok {
			value, known = 1, true
		}
	}
	if body, ok := node.Field("body"); ok {
		part := b.statement(body)
		if node.Kind() == parser.KindDoWhileStatement {
			b.edge(header, part.entry)
			var exits []ID
			for _, exit := range part.exits {
				if !known || value != 0 {
					b.edge(exit, header)
				}
				if !known || value == 0 {
					exits = append(exits, exit)
				}
			}
			for _, next := range part.continues {
				if !known || value != 0 {
					b.edge(next, header)
				}
				if !known || value == 0 {
					exits = append(exits, next)
				}
			}
			exits = append(exits, part.breaks...)
			return fragment{entry: header, exits: exits}
		}
		bodyRuns := !known || value != 0
		if bodyRuns {
			b.edge(header, part.entry)
			for _, exit := range part.exits {
				b.edge(exit, header)
			}
			for _, next := range part.continues {
				b.edge(next, header)
			}
		}
		var exits []ID
		if !known || value == 0 {
			exits = append(exits, header)
		}
		if bodyRuns {
			exits = append(exits, part.breaks...)
		}
		return fragment{entry: header, exits: exits}
	}
	if known && value != 0 {
		return fragment{entry: header}
	}
	return fragment{entry: header, exits: []ID{header}}
}

func (b *builder) condition(node parser.SyntaxNode) (int64, bool) {
	if b.eval == nil {
		return 0, false
	}
	condition, ok := node.Field("condition")
	if !ok {
		return 0, false
	}
	return b.eval(condition)
}

func (b *builder) block(node parser.SyntaxNode) ID {
	id := ID(len(b.graph.Blocks) + 1)
	b.graph.Blocks = append(b.graph.Blocks, Block{
		ID: id, Kind: node.Kind(), Span: node.Range().Span(b.file),
	})
	return id
}

func (b *builder) edge(from, to ID) {
	if from == 0 || to == 0 {
		return
	}
	block := &b.graph.Blocks[from-1]
	for _, existing := range block.Successors {
		if existing == to {
			return
		}
	}
	block.Successors = append(block.Successors, to)
}
