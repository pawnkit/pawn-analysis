package analysis

import (
	"context"

	"github.com/pawnkit/pawn-analysis/preprocess"
	"github.com/pawnkit/pawn-analysis/symbol"
	parser "github.com/pawnkit/pawn-parser"
	"github.com/pawnkit/pawnkit-core/source"
)

// A declaration macro can change the function name as well as the return tag.
// Match the complete original header to its invocation and parse the emitted
// header independently so an unrelated recovery in the expanded file cannot
// hide it. The original body remains the source for diagnostic locations.
func expandedDeclarationTags(ctx context.Context, result *Result) map[source.Span]string {
	pre := result.Preprocess
	if pre.Truncated || len(pre.MacroInvocations) == 0 {
		return nil
	}
	headers := make(map[preprocess.ByteRange]source.Span)
	declarations := result.Parse.Syntax().Declarations()
	for declarations.Next() {
		if ctx.Err() != nil {
			return nil
		}
		node := declarations.Declaration()
		if node.Kind() != parser.KindFunctionDefinition {
			continue
		}
		tag, hasTag := node.Field("tag")
		name, hasName := node.Field("name")
		params, hasParams := node.Field("parameters")
		_, hasState := node.Field("state")
		if !hasTag || !hasName || !hasParams || hasState || tag.Range().Start != node.Range().Start {
			continue
		}
		header := preprocess.ByteRange{Start: tag.Range().Start, End: params.Range().End}
		headers[header] = name.Range().Span(result.File)
	}
	if len(headers) == 0 {
		return nil
	}
	tags := make(map[source.Span]string)
	for _, inv := range pre.MacroInvocations {
		if ctx.Err() != nil {
			return nil
		}
		name, ok := headers[inv.Range]
		if inv.File != 0 || !ok || inv.Expansion.Start == inv.Expansion.End {
			continue
		}
		header := pre.ExpandedSource[inv.Expansion.Start:inv.Expansion.End]
		if tag, ok := parseExpandedHeaderTag(header, result.File); ok {
			tags[name] = tag
		}
	}
	return tags
}

func parseExpandedHeaderTag(header []byte, file source.FileID) (string, bool) {
	text := append(append([]byte(nil), header...), ' ', '{', '}')
	parsed := parser.ParseCompact(text, parser.ParseOptions{})
	if parsed.HasParseErrors() {
		return "", false
	}
	declarations := parsed.Syntax().Declarations()
	for declarations.Next() {
		node := declarations.Declaration()
		if node.Kind() != parser.KindFunctionDefinition {
			continue
		}
		body, hasBody := node.Field("body")
		name, hasName := node.Field("name")
		if !hasBody || body.Range().Start != len(header)+1 || !hasName {
			return "", false
		}
		table := symbol.Build(parsed.Syntax(), file)
		item, ok := table.DeclarationAt(name.Range().Span(file))
		return item.Tag, ok
	}
	return "", false
}
