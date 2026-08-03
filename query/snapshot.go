// Package query provides cached analysis over immutable document snapshots.
package query

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"
	"sync"

	analysis "github.com/pawnkit/pawn-analysis"
	"github.com/pawnkit/pawnkit-core/source"
)

var ErrDocumentNotFound = errors.New("document not found")

// Document is immutable source input.
type Document struct {
	URI     source.URI
	Text    []byte
	Buffer  *source.TextBuffer
	Version int64
}

// Snapshot is an immutable set of versioned documents.
type Snapshot struct {
	mu       sync.Mutex
	docs     map[source.URI]Document
	cache    map[cacheKey]*analysis.Result
	complete map[cacheKey]*analysis.Result
	prior    map[reuseKey]*analysis.Result
}

type cacheKey struct {
	uri     source.URI
	version int64
	options [32]byte
}

type reuseKey struct {
	uri     source.URI
	options [32]byte
}

func New(documents ...Document) *Snapshot {
	s := &Snapshot{
		docs: make(map[source.URI]Document), cache: make(map[cacheKey]*analysis.Result),
		complete: make(map[cacheKey]*analysis.Result), prior: make(map[reuseKey]*analysis.Result),
	}
	for _, document := range documents {
		s.docs[document.URI] = cloneDocument(document)
	}
	return s
}

// Update returns a new snapshot. Stale versions are rejected.
func (s *Snapshot) Update(document Document) (*Snapshot, bool) {
	return s.update(document, true)
}

// UpdateOwned returns a new snapshot without copying document.Text. The caller
// must not modify the slice after this call.
func (s *Snapshot) UpdateOwned(document Document) (*Snapshot, bool) {
	return s.update(document, false)
}

func (s *Snapshot) update(document Document, cloneText bool) (*Snapshot, bool) {
	if s == nil {
		if cloneText {
			return New(document), true
		}
		return newOwned(document), true
	}
	if current, ok := s.docs[document.URI]; ok && document.Version <= current.Version {
		return s, false
	}
	next := &Snapshot{
		docs: make(map[source.URI]Document, len(s.docs)+1), cache: make(map[cacheKey]*analysis.Result),
		complete: make(map[cacheKey]*analysis.Result), prior: make(map[reuseKey]*analysis.Result),
	}
	for uri, current := range s.docs {
		next.docs[uri] = current
	}
	if cloneText {
		document = cloneDocument(document)
	}
	next.docs[document.URI] = document

	s.mu.Lock()
	for key, result := range s.cache {
		if key.uri != document.URI {
			next.cache[key] = result
		} else {
			next.prior[reuseKey{uri: key.uri, options: key.options}] = result
		}
	}
	for key, result := range s.complete {
		if key.uri != document.URI {
			next.complete[key] = result
		}
	}
	s.mu.Unlock()
	return next, true
}

func newOwned(documents ...Document) *Snapshot {
	s := &Snapshot{
		docs: make(map[source.URI]Document), cache: make(map[cacheKey]*analysis.Result),
		complete: make(map[cacheKey]*analysis.Result), prior: make(map[reuseKey]*analysis.Result),
	}
	for _, document := range documents {
		s.docs[document.URI] = document
	}
	return s
}

func (s *Snapshot) Document(uri source.URI) (Document, bool) {
	if s == nil {
		return Document{}, false
	}
	document, ok := s.docs[uri]
	return cloneDocument(document), ok
}

// Analyze returns a cached result for an unchanged document and options.
func (s *Snapshot) Analyze(ctx context.Context, uri source.URI, opts analysis.Options) (*analysis.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	document, ok := s.docs[uri]
	if !ok {
		return nil, ErrDocumentNotFound
	}
	opts.URI = uri
	optionKey := optionsHash(opts)
	key := cacheKey{uri: uri, version: document.Version, options: optionKey}
	s.mu.Lock()
	result := s.cache[key]
	s.mu.Unlock()
	if result != nil {
		return result, nil
	}
	opts.Previous = s.prior[reuseKey{uri: uri, options: optionKey}]
	text := document.Text
	if document.Buffer != nil {
		text = document.Buffer.Bytes()
	}
	result, err := analysis.AnalyzeContext(ctx, text, opts)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if existing := s.cache[key]; existing != nil {
		result = existing
	} else {
		s.cache[key] = result
	}
	s.mu.Unlock()
	return result, nil
}

func cloneDocument(document Document) Document {
	if document.Buffer == nil {
		document.Text = append([]byte(nil), document.Text...)
	}
	return document
}

func optionsHash(opts analysis.Options) [32]byte {
	hash := sha256.New()
	var size [8]byte
	hash.Write([]byte(opts.Revision))
	hash.Write([]byte{0})
	if opts.RetainExpanded {
		hash.Write([]byte{1})
	}
	if opts.SkipSemantics {
		hash.Write([]byte{2})
	}
	if opts.ReuseCompatibleExpansion {
		hash.Write([]byte{3})
	}
	if opts.CollectFunctionFacts {
		hash.Write([]byte{4})
	}
	binary.LittleEndian.PutUint64(size[:], uint64(opts.MaxOutputTokens))
	hash.Write(size[:])
	keys := make([]string, 0, len(opts.Predefined))
	for key := range opts.Predefined {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		binary.LittleEndian.PutUint64(size[:], uint64(len(key)))
		hash.Write(size[:])
		hash.Write([]byte(key))
		value := opts.Predefined[key]
		binary.LittleEndian.PutUint64(size[:], uint64(len(value)))
		hash.Write(size[:])
		hash.Write([]byte(value))
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}
