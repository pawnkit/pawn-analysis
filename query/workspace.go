package query

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"sort"

	analysis "github.com/pawnkit/pawn-analysis"
	"github.com/pawnkit/pawn-analysis/sema"
	"github.com/pawnkit/pawn-analysis/symbol"
	"github.com/pawnkit/pawnkit-core/diagnostic"
	"github.com/pawnkit/pawnkit-core/source"
)

// WorkspaceResult contains analysis for one snapshot revision.
type WorkspaceResult struct {
	Files       map[source.URI]*analysis.Result
	Diagnostics []diagnostic.Diagnostic
}

// AnalyzeWorkspace resolves declarations shared by snapshot documents.
func (s *Snapshot) AnalyzeWorkspace(ctx context.Context, opts analysis.Options) (*WorkspaceResult, error) {
	if s == nil {
		return &WorkspaceResult{Files: make(map[source.URI]*analysis.Result)}, nil
	}
	uris := make([]source.URI, 0, len(s.docs))
	for uri := range s.docs {
		uris = append(uris, uri)
	}
	return s.AnalyzeDocuments(ctx, uris, opts)
}

// AnalyzeDocuments resolves declarations within the selected documents.
func (s *Snapshot) AnalyzeDocuments(ctx context.Context, uris []source.URI, opts analysis.Options) (*WorkspaceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return &WorkspaceResult{Files: make(map[source.URI]*analysis.Result)}, nil
	}

	selected := make(map[source.URI]struct{}, len(uris))
	unique := make([]source.URI, 0, len(uris))
	for _, uri := range uris {
		if _, ok := s.docs[uri]; !ok {
			return nil, ErrDocumentNotFound
		}
		if _, exists := selected[uri]; exists {
			continue
		}
		selected[uri] = struct{}{}
		unique = append(unique, uri)
	}
	uris = unique
	sort.Slice(uris, func(i, j int) bool { return uris[i].String() < uris[j].String() })

	resolver := newWorkspaceResolver(opts.Names)
	prepared := make(map[source.URI]*analysis.Result, len(uris))
	for _, uri := range uris {
		indexOpts := opts
		indexOpts.RetainExpanded = true
		indexOpts.SkipSemantics = true
		result, err := s.Analyze(ctx, uri, indexOpts)
		if err != nil {
			return nil, err
		}
		prepared[uri] = result
		table := result.ExpandedSymbols
		if table == nil {
			table = result.Symbols
		}
		resolver.add(table)
	}

	workspace := &WorkspaceResult{Files: make(map[source.URI]*analysis.Result, len(uris))}
	for _, uri := range uris {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fileOpts := opts
		fileOpts.URI = uri
		fileOpts.Names = resolver
		document := s.docs[uri]
		key := cacheKey{
			uri: uri, version: document.Version,
			options: completeOptionsHash(fileOpts, resolver.fingerprint()),
		}
		s.mu.Lock()
		result := s.complete[key]
		s.mu.Unlock()
		if result == nil {
			var err error
			result, err = analysis.CompleteContext(ctx, prepared[uri], fileOpts)
			if err != nil {
				return nil, err
			}
			s.mu.Lock()
			if existing := s.complete[key]; existing != nil {
				result = existing
			} else {
				s.complete[key] = result
			}
			s.mu.Unlock()
		}
		workspace.Files[uri] = result
		workspace.Diagnostics = append(workspace.Diagnostics, result.Diagnostics...)
	}
	return workspace, nil
}

func completeOptionsHash(opts analysis.Options, resolver [32]byte) [32]byte {
	base := optionsHash(opts)
	hash := sha256.New()
	hash.Write(base[:])
	hash.Write(resolver[:])
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

type workspaceResolver struct {
	names            map[string]sema.Callable
	nonCalls         map[string]struct{}
	fallback         sema.Resolver
	fingerprintCache [32]byte
	fingerprintValid bool
}

func (r *workspaceResolver) fingerprint() [32]byte {
	if r.fingerprintValid {
		return r.fingerprintCache
	}
	hash := sha256.New()
	var size [8]byte
	write := func(value string) {
		binary.LittleEndian.PutUint64(size[:], uint64(len(value)))
		hash.Write(size[:])
		hash.Write([]byte(value))
	}
	callables := make([]string, 0, len(r.names))
	for name := range r.names {
		callables = append(callables, name)
	}
	sort.Strings(callables)
	for _, name := range callables {
		callable := r.names[name]
		hash.Write([]byte{1})
		write(name)
		write(callable.ReturnTag)
		binary.LittleEndian.PutUint64(size[:], uint64(callable.MinArgs))
		hash.Write(size[:])
		binary.LittleEndian.PutUint64(size[:], uint64(callable.MaxArgs))
		hash.Write(size[:])
		for _, tag := range callable.ParamTags {
			write(tag)
		}
	}
	names := make([]string, 0, len(r.nonCalls))
	for name := range r.nonCalls {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		hash.Write([]byte{2})
		write(name)
	}
	copy(r.fingerprintCache[:], hash.Sum(nil))
	r.fingerprintValid = true
	return r.fingerprintCache
}

func newWorkspaceResolver(fallback sema.Resolver) *workspaceResolver {
	return &workspaceResolver{
		names: make(map[string]sema.Callable), nonCalls: make(map[string]struct{}), fallback: fallback,
	}
}

func (r *workspaceResolver) add(table *symbol.Table) {
	if table == nil {
		return
	}
	for _, item := range table.Symbols {
		scope, ok := table.Scope(item.Scope)
		if !ok || scope.Kind != symbol.ScopeFile {
			continue
		}
		if item.Kind.IsCallable() {
			r.names[item.Name] = sema.Callable{
				ReturnTag: item.Tag, ParamTags: append([]string(nil), item.ParamTags...),
				MinArgs: item.MinArgs, MaxArgs: item.MaxArgs,
			}
		} else {
			r.nonCalls[item.Name] = struct{}{}
		}
	}
	r.fingerprintValid = false
}

func (r *workspaceResolver) ResolveName(name string) sema.NameState {
	if _, ok := r.names[name]; ok {
		return sema.NameFound
	}
	if _, ok := r.nonCalls[name]; ok {
		return sema.NameFound
	}
	if r.fallback != nil {
		return r.fallback.ResolveName(name)
	}
	return sema.NameUnknown
}

func (r *workspaceResolver) ResolveCallable(name string) (sema.Callable, bool) {
	callable, ok := r.names[name]
	if ok {
		return callable, true
	}
	if fallback, ok := r.fallback.(sema.CallableResolver); ok {
		return fallback.ResolveCallable(name)
	}
	return sema.Callable{}, false
}
