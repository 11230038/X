package kb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"backend/internal/authz"
	"backend/internal/embed"
	"backend/internal/event"
	"backend/internal/parse"
)

const (
	// maxNameChars is the knowledge base name column's width, in characters,
	// which is what a name is measured in here as everywhere else in this
	// package.
	maxNameChars = 255
	// windowChars is the window a text read falls back to when the caller does
	// not ask for one: about four chunks, which is enough to answer a question
	// about a passage without carrying the document.
	windowChars = 2000
)

// Service owns the knowledge base domain: collections, the documents parsed
// into them, and the retrieval that reads them back.
type Service struct {
	repository Repository
	parsers    *parse.Registry
	files      FileSource
	embedder   embed.Embedder
	logger     *slog.Logger
	options    Options
}

// NewService wires the domain to its storage, its parser registry, the file
// library behind the FileSource port, and the embedding provider.
func NewService(options Options, repository Repository, parsers *parse.Registry, files FileSource, embedder embed.Embedder) (*Service, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	switch {
	case repository == nil:
		return nil, fmt.Errorf("kb: a repository is required")
	case parsers == nil:
		return nil, fmt.Errorf("kb: a parser registry is required")
	case files == nil:
		return nil, fmt.Errorf("kb: a file source is required")
	case embedder == nil:
		// Required even though retrieval survives without it: the index worker
		// cannot, and a service built without one would look healthy while every
		// base's vectors stayed NULL.
		return nil, fmt.Errorf("kb: an embedder is required")
	}
	return &Service{
		repository: repository, parsers: parsers, files: files, embedder: embedder,
		logger: options.Logger, options: options,
	}, nil
}

// CreateKnowledgeBase creates one collection.
//
// provider is written as the one this build has and the embedding model as the
// one this process embeds with: KB-02 binds both at creation, so that a base
// created under one model still says so after the configuration moves on — and
// the startup check has something to disagree with.
func (s *Service) CreateKnowledgeBase(ctx context.Context, name, description string, caller authz.Caller) (KnowledgeBase, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return KnowledgeBase{}, fmt.Errorf("%w: a knowledge base needs a name", ErrInvalidInput)
	}
	if utf8.RuneCountInString(name) > maxNameChars {
		return KnowledgeBase{}, fmt.Errorf("%w: the name is longer than %d characters", ErrInvalidInput, maxNameChars)
	}
	id, err := newID(kbIDPrefix)
	if err != nil {
		return KnowledgeBase{}, fmt.Errorf("generate knowledge base id: %w", err)
	}
	now := time.Now().UTC()
	record := KnowledgeBase{
		ID: id, Name: name, Description: strings.TrimSpace(description),
		Provider: ProviderNative, EmbeddingModel: s.options.EmbeddingModel,
		OwnerID: caller.UserID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repository.CreateKnowledgeBase(ctx, record); err != nil {
		return KnowledgeBase{}, err
	}
	// Read back rather than return what was sent: whether this base is the
	// default one is decided in storage, together with the lock that keeps it
	// the only one. The read-back is scoped, so a base stored with no owner —
	// which only happens for an unauthenticated caller, who cannot reach this —
	// would come back as not found rather than as a collection nobody can list.
	return s.repository.KnowledgeBase(ctx, id, caller)
}

// KnowledgeBases lists the caller's own collections, the default one first.
func (s *Service) KnowledgeBases(ctx context.Context, caller authz.Caller) ([]KnowledgeBase, error) {
	return s.repository.KnowledgeBases(ctx, caller)
}

// KnowledgeBase returns one collection. A collection belonging to another
// account is ErrNotFound, not a refusal: the id might as well not exist.
func (s *Service) KnowledgeBase(ctx context.Context, kbID string, caller authz.Caller) (KnowledgeBase, error) {
	return s.repository.KnowledgeBase(ctx, strings.TrimSpace(kbID), caller)
}

// SetDefaultKnowledgeBase moves the default mark.
//
// The mark changes nothing about retrieval — a turn's scope decides what is
// searched — and it is not a fallback for a scope that lost a base: KB-07's
// "report it, do not substitute" is about what a turn reads, not about which
// base is starred in a list.
func (s *Service) SetDefaultKnowledgeBase(ctx context.Context, kbID string, caller authz.Caller) error {
	return s.repository.SetDefaultKnowledgeBase(ctx, strings.TrimSpace(kbID), caller)
}

// DeleteKnowledgeBase removes a collection and its documents.
func (s *Service) DeleteKnowledgeBase(ctx context.Context, kbID string, caller authz.Caller) error {
	return s.repository.DeleteKnowledgeBase(ctx, strings.TrimSpace(kbID), caller)
}

// Documents lists a collection's documents.
func (s *Service) Documents(ctx context.Context, kbID string, caller authz.Caller) ([]Document, error) {
	return s.repository.Documents(ctx, strings.TrimSpace(kbID), caller)
}

// Document returns one document.
func (s *Service) Document(ctx context.Context, documentID string, caller authz.Caller) (Document, error) {
	return s.repository.Document(ctx, strings.TrimSpace(documentID), caller)
}

// DocumentOfFile returns a document parsed from one file, for a caller that
// knows the file rather than the place it is used (ATT-02's preview).
func (s *Service) DocumentOfFile(ctx context.Context, fileID string, caller authz.Caller) (Document, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return Document{}, fmt.Errorf("%w: a document is read by the file it came from", ErrInvalidInput)
	}
	return s.repository.DocumentOfFile(ctx, fileID, caller)
}

// ChunkSpans returns where the chunk boundaries of a document's active revision
// fall, which is what a preview of the text marks and what a citation's
// character span refers to.
func (s *Service) ChunkSpans(ctx context.Context, documentID string, caller authz.Caller) ([]ChunkSpan, error) {
	document, err := s.repository.Document(ctx, strings.TrimSpace(documentID), caller)
	if err != nil {
		return nil, err
	}
	return s.repository.ChunkSpans(ctx, document.ID, document.ActiveRevision)
}

// RemoveDocument removes one document.
func (s *Service) RemoveDocument(ctx context.Context, documentID string, caller authz.Caller) error {
	return s.repository.DeleteDocument(ctx, strings.TrimSpace(documentID), caller)
}

// AddDocument parses a stored file into a knowledge base and chunks it.
//
// It is synchronous, and only up to the parse: extraction and chunking are
// seconds, while embedding a document at the character limit is hundreds of
// provider calls. So the full-text path is usable the moment this returns, and
// the vectors arrive behind it. Making the cheap half asynchronous would mean
// "uploaded, now wait a while before you can search it" for no gain; making the
// expensive half synchronous would mean timeouts.
//
// An empty kbID makes the document an attachment: the same parse, kept for the
// turns that carried the file and retrievable by nobody.
func (s *Service) AddDocument(ctx context.Context, fileID, kbID string, caller authz.Caller) (Document, error) {
	fileID, kbID = strings.TrimSpace(fileID), strings.TrimSpace(kbID)
	if fileID == "" {
		return Document{}, fmt.Errorf("%w: a document needs a file", ErrInvalidInput)
	}
	if kbID != "" {
		if _, err := s.repository.KnowledgeBase(ctx, kbID, caller); err != nil {
			return Document{}, err
		}
	}
	// The same file in the same base is the same document, parsed once. Two
	// turns carrying one attachment must not each pay for a parse, and a base
	// holding one file twice would return the same text as two sources.
	existing, err := s.repository.DocumentForFile(ctx, fileID, kbID, caller)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Document{}, err
	}

	documentID, err := newID(docIDPrefix)
	if err != nil {
		return Document{}, fmt.Errorf("generate document id: %w", err)
	}
	write, err := s.extract(ctx, fileID, kbID, documentID, 1, caller)
	if err != nil {
		return Document{}, err
	}
	// CreateDocument is the one write in this package that takes no caller. The
	// two things it attaches to have both just been read as the caller's — the
	// base above, the file inside extract — so there is no earlier row left for
	// a caller to smuggle past.
	if err := s.repository.CreateDocument(ctx, write); err != nil {
		// Two callers added the same file at once. The loser's work is thrown
		// away and the winner's document returned: the request was "make sure
		// this file is in this base", and it now is.
		//
		// This branch is reachable because the schema says so — the unique index
		// on (file_id, file_scope) refuses the second write (migration 00008).
		// Without it both writes succeeded and the base held the file twice.
		if errors.Is(err, ErrConflict) {
			return s.repository.DocumentForFile(ctx, fileID, kbID, caller)
		}
		return Document{}, err
	}
	return write.Document, nil
}

// RebuildDocument re-parses a document and switches it to a new revision.
//
// It is also the only way to embed under a different model, and the only way
// to retry a document whose vectors failed: the new revision's chunks have no
// vectors, so the index worker picks the document up again.
//
// The parse happens before the switch, and the switch is one transaction, so a
// rebuild that fails leaves the document's current revision serving retrieval
// exactly as it was.
func (s *Service) RebuildDocument(ctx context.Context, documentID string, caller authz.Caller) (Document, error) {
	documentID = strings.TrimSpace(documentID)
	document, err := s.repository.Document(ctx, documentID, caller)
	if err != nil {
		return Document{}, err
	}
	revision := document.ActiveRevision + 1
	write, err := s.extract(ctx, document.FileID, document.KBID, documentID, revision, caller)
	if err != nil {
		return Document{}, err
	}
	if err := s.repository.ReplaceRevision(ctx, write, document.ActiveRevision, caller); err != nil {
		return Document{}, err
	}
	return write.Document, nil
}

// extract reads a file and splits what came back, returning the row and the
// chunks of one revision without writing either.
//
// The caller reaches the file library through here as well as through the base:
// opening bytes is what turns "parse this file id into my collection" into a
// read of somebody else's upload, so the account travels with the request all
// the way down to the port rather than being re-derived from the base, which an
// attachment does not have.
func (s *Service) extract(ctx context.Context, fileID, kbID, documentID string, revision int, caller authz.Caller) (DocumentWrite, error) {
	file, err := s.files.Open(ctx, fileID, caller)
	if err != nil {
		return DocumentWrite{}, err
	}
	defer func() { _ = file.Content.Close() }()

	filename := strings.TrimSpace(file.Filename)
	if filename == "" {
		// documents.filename is neither null nor blank, and the file id is the
		// only other name this file has.
		filename = fileID
	}
	result, parseErr := s.parsers.Parse(ctx, parse.Request{
		Filename: filename, MIME: file.MIMEType, Category: file.Category,
		Content: file.Content, MaxChars: s.options.MaxCharsPerDocument,
	})
	truncated := false
	switch code := parse.CodeOf(parseErr); {
	case parseErr == nil:
	case code == parse.CodeParseTruncated:
		// The one failure that arrives with a usable document. The text that
		// fits is kept and the row says it was cut, which is what ATT-04 asks
		// for: never a silent [:N].
		truncated = true
		if s.logger != nil {
			s.logger.WarnContext(ctx, "document truncated at the character limit",
				"document_id", documentID, "file_id", fileID, "error", parseErr.Error())
		}
	default:
		return DocumentWrite{}, parseErr
	}
	// A caller that went away is not a document that failed to parse, and the
	// parse error above is not what it should hear about.
	if err := ctx.Err(); err != nil {
		return DocumentWrite{}, err
	}

	text := result.Text
	chunks, err := Split(text, s.options.Chunking)
	if err != nil {
		return DocumentWrite{}, err
	}
	now := time.Now().UTC()
	document := Document{
		ID: documentID, KBID: kbID, FileID: fileID, Filename: filename,
		Engine: result.Engine, TextChars: int64(utf8.RuneCountInString(text)),
		Truncated: truncated, ChunkSize: s.options.Chunking.Size,
		ChunkOverlap: s.options.Chunking.Overlap, ActiveRevision: revision,
		// Always pending, attachments included. A pending row is one whose
		// vectors are not computed, which is exactly true of an attachment that
		// will never get any; the rule that says so is in the worker's claim
		// (it only claims documents that belong to a base), where it belongs,
		// rather than in a state that would have to mean two things.
		VectorState: VectorStatePending,
		CreatedAt:   now, UpdatedAt: now,
	}
	return DocumentWrite{
		Document: document, Text: text,
		Chunks: chunksOf(documentID, kbID, revision, chunks),
	}, nil
}

// Retrieve runs one hybrid search over a turn's knowledge base scope.
//
// Neither path can fail the other: a query that cannot be embedded is a missing
// vector branch, and the full-text branch alone still answers — the fusion
// degenerates rather than breaking, which is the same property that lets a
// brand-new document be retrieved before its vectors exist.
func (s *Service) Retrieve(ctx context.Context, input RetrieveInput) (Retrieval, error) {
	query := strings.TrimSpace(input.Query)
	if utf8.RuneCountInString(query) < minQueryChars {
		return Retrieval{}, fmt.Errorf("%w: it needs at least %d characters", ErrQueryTooShort, minQueryChars)
	}
	if len(input.KBIDs) == 0 {
		return Retrieval{}, fmt.Errorf("%w: retrieval needs at least one knowledge base", ErrInvalidInput)
	}
	topK := input.TopK
	if topK <= 0 {
		topK = s.options.RetrievalTopK
	}

	live, unavailable, err := s.liveScope(ctx, input.KBIDs, input.Caller)
	if err != nil {
		return Retrieval{}, err
	}
	if len(live) == 0 {
		// Every base in the scope is gone. That is a report, not a search and not
		// a substitution: nothing is retrieved from anywhere else.
		return Retrieval{Unavailable: unavailable}, nil
	}

	branches := make([]Branch, 0, 2)
	vector, err := s.queryVector(ctx, input.Vector, query)
	if err != nil {
		return Retrieval{}, err
	}
	if vector != nil {
		ids, err := s.repository.SearchVector(ctx, live, vector, s.options.Candidates)
		if err != nil {
			return Retrieval{}, err
		}
		branches = append(branches, Branch{Name: "vector", ChunkIDs: ids})
	}
	ids, err := s.repository.SearchFullText(ctx, live, query, s.options.Candidates)
	if err != nil {
		return Retrieval{}, err
	}
	branches = append(branches, Branch{Name: "fulltext", ChunkIDs: ids})

	fused, err := Fuse(branches, s.options.RRFK, topK)
	if err != nil {
		return Retrieval{}, err
	}
	if len(fused) == 0 {
		return Retrieval{Unavailable: unavailable}, nil
	}

	chunkIDs := make([]string, 0, len(fused))
	for _, entry := range fused {
		chunkIDs = append(chunkIDs, entry.ChunkID)
	}
	rows, err := s.repository.ChunkSources(ctx, chunkIDs)
	if err != nil {
		return Retrieval{}, err
	}
	byID := make(map[string]ChunkSource, len(rows))
	for _, row := range rows {
		byID[row.ChunkID] = row
	}
	sources := make([]event.Source, 0, len(fused))
	for _, entry := range fused {
		row, ok := byID[entry.ChunkID]
		if !ok {
			// A chunk the search found and the read did not: the document was
			// rebuilt in between, so this text is no longer the document's. It
			// is left out rather than cited at a revision that has moved on.
			continue
		}
		sources = append(sources, sourceOf(row, len(sources)+1, entry.Score, input.SnippetChars))
	}
	return Retrieval{Sources: sources, Unavailable: unavailable}, nil
}

// queryVector returns the vector to search with, or nil when there is none to
// be had.
//
// Failing to embed the query is recorded and degraded, never raised: the answer
// that comes back without it is less well recalled, while an error would take
// away a document library that is otherwise working. Cancellation is the one
// exception — the caller's own decision is not a degradation to swallow.
func (s *Service) queryVector(ctx context.Context, supplied embed.Vector, query string) (embed.Vector, error) {
	if len(supplied) > 0 {
		return supplied, nil
	}
	result, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if s.logger != nil {
			s.logger.WarnContext(ctx, "retrieving without the vector path",
				"error", err.Error())
		}
		return nil, nil
	}
	if len(result.Vectors) == 0 {
		return nil, nil
	}
	return result.Vectors[0], nil
}

// liveScope splits the requested bases into the ones that exist and the ones
// that no longer do. Both lists matter: the first is what may be searched, and
// the second is what the model and the user have to be told about (KB-07).
func (s *Service) liveScope(ctx context.Context, kbIDs []string, caller authz.Caller) (live, unavailable []string, err error) {
	seen := make(map[string]bool, len(kbIDs))
	for _, kbID := range kbIDs {
		kbID = strings.TrimSpace(kbID)
		if kbID == "" || seen[kbID] {
			continue
		}
		seen[kbID] = true
		// A base another account owns lands in unavailable with the ones that are
		// gone, which is the right shape for this rule and worth being explicit
		// about: KB-07 says a scope that lost a base is reported, not quietly
		// substituted, and from where this caller stands a base they may not read
		// is one they do not have. Searching it would be the substitution KB-07
		// forbids, only worse.
		if _, err := s.repository.KnowledgeBase(ctx, kbID, caller); err != nil {
			if errors.Is(err, ErrNotFound) {
				unavailable = append(unavailable, kbID)
				continue
			}
			return nil, nil, err
		}
		live = append(live, kbID)
	}
	return live, unavailable, nil
}

// ReadWindow returns a window of a document's extracted text, clamped to what
// the document has.
//
// It reads the window out of storage rather than out of a loaded document: at
// the configured limit a document is two hundred thousand characters, and
// opening a citation at character 6144 must not cost that. The offsets are
// characters, like every other offset here, so a window and a citation's
// char_start describe the same thing.
func (s *Service) ReadWindow(ctx context.Context, documentID string, offset, limit int64, caller authz.Caller) (Window, error) {
	document, err := s.repository.Document(ctx, strings.TrimSpace(documentID), caller)
	if err != nil {
		return Window{}, err
	}
	if offset < 0 {
		return Window{}, fmt.Errorf("%w: the offset must not be negative", ErrInvalidInput)
	}
	if limit <= 0 {
		limit = windowChars
	}
	if offset >= document.TextChars {
		// Past the end is an empty window at the end, not an error: a citation
		// whose document was replaced by a shorter one should open on nothing
		// rather than fail.
		return Window{
			Document: document, CharStart: document.TextChars,
			CharEnd: document.TextChars, TotalChars: document.TextChars,
		}, nil
	}
	if offset+limit > document.TextChars {
		limit = document.TextChars - offset
	}
	text, err := s.repository.DocumentText(ctx, document.ID, offset, limit)
	if err != nil {
		return Window{}, err
	}
	return Window{
		Document: document, Text: text,
		CharStart: offset, CharEnd: offset + int64(utf8.RuneCountInString(text)),
		TotalChars: document.TextChars,
	}, nil
}
