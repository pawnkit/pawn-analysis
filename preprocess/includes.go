package preprocess

import (
	"github.com/pawnkit/pawn-parser/lexer"
	"github.com/pawnkit/pawn-parser/token"
	"github.com/pawnkit/pawnkit-core/diagnostic"
)

func (e *engine) handleInclude(f *frame, hash token.Token, optional bool) {
	kwTok := f.toks[f.pos-1]
	searchStart := kwTok.End.Offset
	i := searchStart
	for i < len(f.source) && (f.source[i] == ' ' || f.source[i] == '\t') {
		i++
	}
	if i >= len(f.source) || (f.source[i] != '<' && f.source[i] != '"') {
		if f.currentActive() {
			e.diag(f, CodeMalformedInclude, diagnostic.SeverityError, "malformed #include directive", spanOf(hash, kwTok))
		}
		e.resyncAndFinishLine(f, i)
		return
	}

	angle := f.source[i] == '<'
	closer := byte('"')
	if angle {
		closer = '>'
	}
	end := i + 1
	for end < len(f.source) && f.source[end] != closer && f.source[end] != '\n' {
		end++
	}
	if end >= len(f.source) || f.source[end] != closer {
		if f.currentActive() {
			e.diag(f, CodeMalformedInclude, diagnostic.SeverityError, "unterminated #include path", spanOf(hash, kwTok))
		}
		e.resyncAndFinishLine(f, end)
		return
	}
	path := string(f.source[i+1 : end])
	directiveEnd := end + 1

	e.resyncAndFinishLine(f, directiveEnd)

	if !f.currentActive() {
		return
	}

	inc := Include{
		File: f.fileIndex, Path: path, Angle: angle, Optional: optional,
		DirectiveSpan: ByteRange{Start: hash.Start.Offset, End: directiveEnd}, Active: true,
	}

	if e.opts.Resolver != nil {
		content, uri, ok := e.opts.Resolver.Resolve(f.uri, path, angle)
		switch {
		case !ok && !optional:
			e.diag(f, CodeIncludeNotFound, diagnostic.SeverityError, "include target not found: "+path, inc.DirectiveSpan)
		case ok && e.includeStack[uri]:
			e.diag(f, CodeIncludeCycle, diagnostic.SeverityError, "include cycle detected: "+uri, inc.DirectiveSpan)
			inc.Resolved = true
			inc.ResolvedURI = uri
		case ok && f.depth+1 > e.opts.MaxIncludeDepth:
			e.truncated = true
			e.diag(f, CodeIncludeDepthLimit, diagnostic.SeverityError, "maximum include depth exceeded", inc.DirectiveSpan)
			inc.Resolved = true
			inc.ResolvedURI = uri
		case ok:
			inc.Resolved = true
			inc.ResolvedURI = uri
			childIndex := uint32(len(e.files)) //nolint:gosec // File count is bounded by include depth.
			e.files = append(e.files, FileInfo{URI: uri, Depth: f.depth + 1, Content: content})
			inc.ChildFile = childIndex
			inc.HasChildFile = true

			e.includeStack[uri] = true
			child := &frame{
				fileIndex: childIndex, source: content, toks: lexer.Tokenize(content),
				uri: uri, depth: f.depth + 1, lineStart: true,
			}
			e.run(child)
			delete(e.includeStack, uri)
		}
	}

	e.includes = append(e.includes, inc)
}

// resyncAndFinishLine advances f past byteOffset (consumed via raw source
// scanning rather than the token stream, since a bracketed include path
// like <a_samp> does not tokenize as a single unit) and then finishes
// consuming the remainder of the logical line normally.
func (e *engine) resyncAndFinishLine(f *frame, byteOffset int) {
	for !f.atEnd() && f.toks[f.pos].Start.Offset < byteOffset {
		f.pos++
	}
	if f.pos > 0 {
		f.lineStart = endsLine(f.toks[f.pos-1])
	}
	if !f.lineStart {
		e.collectRestOfLine(f)
	}
}
