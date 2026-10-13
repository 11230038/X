package kb

// The index worker: the process that fills in the vectors behind a document
// that is already readable.
//
// Everything here exists because the vectors arrive after the text does. If
// embedding were synchronous, a document would be searchable only when it was
// completely indexed, and a walk through the queue — the claim, the lease, the
// retry — would have no reason to exist.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"backend/internal/embed"
)

const (
	// defaultPollInterval is how long an idle worker waits before looking for
	// work again. It is the delay a user feels between "the upload finished" and
	// "the semantic search finds it", so it is short; it is not zero because an
	// idle worker looking in a tight loop costs a query every few milliseconds
	// for nothing.
	defaultPollInterval = 2 * time.Second
	// maxPollInterval caps the widening below. Past it the worker has decided
	// the provider is having a bad time and is waiting it out rather than
	// hammering it.
	maxPollInterval = 30 * time.Second
	// indexOwnerPrefix names a claim, so that documents.vector_owner says which
	// kind of thing is looking at the document.
	indexOwnerPrefix = "idx_"
	// errorMessageChars is the width of documents.vector_error, in characters.
	// A message longer than this is cut here rather than refused by the column:
	// losing the tail of an error message is cheaper than losing the fact that
	// the document failed.
	errorMessageChars = 128
	// releaseTimeout bounds the write that hands a claim back while the process
	// is on its way out. It is deliberately not the caller's deadline: the whole
	// point of this write is that it happens when the caller's context is
	// already gone.
	releaseTimeout = 5 * time.Second
)

// WorkerOptions configure the index worker.
type WorkerOptions struct {
	// Lease is how long a claim is good for. A worker that dies mid-document
	// leaves the row claimed, and this is how long that row waits before the
	// next worker takes it over.
	Lease time.Duration
	// Workers is how many documents are embedded at once. It bounds the request
	// rate against the provider, not the work: a single document is embedded by
	// one worker in provider-sized requests.
	Workers int
	// EmbedBatchSize is how many chunk texts one provider call asks for, and
	// therefore also the size of the group that is stored in one transaction. It
	// is the client's batch size: a group larger than that would be split by the
	// client into requests whose results are thrown away together, which is the
	// opposite of what the grouping is for.
	EmbedBatchSize int
	// Poll is how long an idle worker waits before looking again. Zero means
	// defaultPollInterval.
	Poll   time.Duration
	Logger *slog.Logger
}

// Validate checks the settings a worker cannot run without.
func (o WorkerOptions) Validate() error {
	if o.Lease <= 0 {
		return fmt.Errorf("kb: the vector lease must be a positive duration")
	}
	if o.Workers <= 0 {
		return fmt.Errorf("kb: the index worker count must be a positive integer")
	}
	if o.EmbedBatchSize <= 0 {
		return fmt.Errorf("kb: the embedding batch size must be a positive integer")
	}
	if o.Poll < 0 {
		return fmt.Errorf("kb: the poll interval must not be negative")
	}
	return nil
}

// Worker fills in the vectors of the documents that belong to a knowledge base.
type Worker struct {
	repository Repository
	embedder   embed.Embedder
	options    WorkerOptions
	logger     *slog.Logger
	// owner is this worker's name on every claim it takes. It is what makes a
	// claim exclusive across processes as well as within one: two processes
	// starting with the same owner string would both believe they own whatever
	// the other claimed, so it is generated rather than configured.
	owner string
}

// NewWorker wires the queue reader to the provider.
func NewWorker(options WorkerOptions, repository Repository, embedder embed.Embedder) (*Worker, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if repository == nil {
		return nil, fmt.Errorf("kb: an index worker needs a repository")
	}
	if embedder == nil {
		return nil, fmt.Errorf("kb: an index worker needs an embedder")
	}
	owner, err := newID(indexOwnerPrefix)
	if err != nil {
		return nil, fmt.Errorf("generate the index worker name: %w", err)
	}
	if options.Poll == 0 {
		options.Poll = defaultPollInterval
	}
	return &Worker{
		repository: repository, embedder: embedder,
		options: options, logger: options.Logger, owner: owner,
	}, nil
}

// Run embeds documents until ctx is done, then waits for the workers to stop.
//
// The wait is the point of it returning rather than detaching: a worker stops
// between two groups, and the group it is in the middle of is stored before it
// looks up. A shutdown that waits here therefore loses no work that was already
// paid for — only the time.
func (w *Worker) Run(ctx context.Context) {
	var running sync.WaitGroup
	for index := 0; index < w.options.Workers; index++ {
		running.Add(1)
		go func() {
			defer running.Done()
			w.work(ctx)
		}()
	}
	running.Wait()
}

// IndexOne claims one document and fills in its vectors, reporting whether
// there was a document to work on.
//
// It is the whole of what a worker does in one call, so that the loop above is
// only scheduling and a test can drive the work a document at a time instead of
// starting goroutines and waiting.
func (w *Worker) IndexOne(ctx context.Context) (bool, error) {
	if ctx.Err() != nil {
		// Asked to stop: not claiming is how this worker avoids a request the
		// caller has already said not to make.
		return false, nil
	}
	document, err := w.repository.ClaimDocument(ctx, w.owner, w.options.Lease, time.Now().UTC())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		w.log(ctx, "claiming a document to index failed", Document{}, err)
		return false, err
	}
	if err := w.index(ctx, document); err != nil {
		w.log(ctx, "indexing a document failed", document, err)
		return true, err
	}
	return true, nil
}

// index embeds the chunks a document is still missing vectors for, in groups,
// and ends the claim.
func (w *Worker) index(ctx context.Context, document Document) error {
	pending, err := w.repository.PendingChunks(ctx, document.ID, document.ActiveRevision)
	if err != nil {
		return w.handBack(ctx, document, err)
	}
	// A document with nothing pending is one a previous worker finished
	// embedding and did not get to mark — it is done, and saying so is the
	// whole of this pass.
	for start := 0; start < len(pending); start += w.options.EmbedBatchSize {
		end := min(start+w.options.EmbedBatchSize, len(pending))
		group := pending[start:end]

		texts := make([]string, len(group))
		for index, chunk := range group {
			texts[index] = chunk.Content
		}
		result, err := w.embedder.Embed(ctx, texts)
		if err != nil {
			return w.handBack(ctx, document, err)
		}
		if len(result.Vectors) != len(texts) {
			// Nothing is stored against a group whose answer does not line up
			// with the question: vectors written onto the wrong chunks are
			// wrong for the life of the revision, and wrong silently.
			return w.handBack(ctx, document, fmt.Errorf(
				"the provider returned %d vectors for %d chunks", len(result.Vectors), len(texts)))
		}
		stored := make([]ChunkRecord, len(group))
		for index, chunk := range group {
			stored[index] = ChunkRecord{ChunkID: chunk.ChunkID, Embedding: result.Vectors[index].String()}
		}
		if err := w.repository.SaveEmbeddings(ctx, stored); err != nil {
			return w.handBack(ctx, document, err)
		}
		if ctx.Err() != nil {
			// The group that just landed stays landed; the rest waits for the
			// next worker, which is why stopping between groups costs nothing
			// but time.
			return w.handBack(ctx, document, ctx.Err())
		}
	}
	return w.complete(ctx, document)
}

// complete marks a document as embedded.
func (w *Worker) complete(ctx context.Context, document Document) error {
	err := w.repository.CompleteDocument(ctx, document.ID, w.owner)
	if err != nil && !errors.Is(err, ErrConflict) {
		return err
	}
	// A conflict is not a failure here: it means the claim was taken away while
	// this worker was embedding — a rebuild, or a lease that ran out — and the
	// revision just embedded is no longer the one being served. Whoever holds
	// the document now has a revision of their own to embed.
	return nil
}

// handBack ends a claim that did not finish, and decides what the document says
// about why.
func (w *Worker) handBack(ctx context.Context, document Document, cause error) error {
	stopping := ctx.Err() != nil
	// The release runs on a context that outlives the caller's. The claim is the
	// one thing this worker still owns, and a shutdown is exactly when leaving it
	// behind costs the lease's worth of delay.
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()

	var provider *embed.HTTPError
	if errors.As(cause, &provider) && !provider.Retryable() {
		// The provider read the request and refused it. Asking again gets the
		// same answer, so the document is parked with the reason and waits for a
		// rebuild — which is also the documented way to move a base onto a
		// different embedding model.
		if err := w.repository.FailDocument(releaseCtx, document.ID, w.owner, errorMessage(cause)); err != nil {
			return errors.Join(cause, err)
		}
		return fmt.Errorf("index document %s: %w", document.ID, cause)
	}
	// Everything else is worth another attempt: a provider that is unwell, a
	// database that is, a worker that is stopping. The document goes back to the
	// queue with the reason kept — except when the worker is the reason, where
	// the message would say "context canceled" and mean nothing to a reader.
	message := ""
	if !stopping {
		message = errorMessage(cause)
	}
	if err := w.repository.RetryDocument(releaseCtx, document.ID, w.owner, message); err != nil {
		return errors.Join(cause, err)
	}
	return fmt.Errorf("index document %s: %w", document.ID, cause)
}

// work is one worker's loop.
func (w *Worker) work(ctx context.Context) {
	failures := 0
	for {
		worked, err := w.IndexOne(ctx)
		switch {
		case err != nil:
			// A failure is asked again rather than hammered, and the widening is
			// per worker: four workers against a provider that is down back off
			// together, not in lockstep.
			failures++
			if !waitFor(ctx, widen(w.options.Poll, failures)) {
				return
			}
		case worked:
			// There is more where that came from, so look again at once rather
			// than making the queue wait out a poll interval per document.
			failures = 0
		default:
			failures = 0
			if !waitFor(ctx, w.options.Poll) {
				return
			}
		}
	}
}

// widen returns how long to wait after this many failures in a row.
func widen(base time.Duration, failures int) time.Duration {
	shift := failures - 1
	if shift <= 0 {
		return base
	}
	if shift > 4 {
		shift = 4
	}
	wait := base << shift
	if wait > maxPollInterval {
		return maxPollInterval
	}
	return wait
}

// waitFor sleeps, and reports whether it slept: false means the caller has
// stopped and so should the worker.
func waitFor(ctx context.Context, wait time.Duration) bool {
	if wait <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// errorMessage fits a failure into the column that holds it.
func errorMessage(err error) string {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "the provider gave no reason"
	}
	if utf8.RuneCountInString(message) <= errorMessageChars {
		return message
	}
	return string([]rune(message)[:errorMessageChars-1]) + "…"
}

func (w *Worker) log(ctx context.Context, message string, document Document, err error) {
	if w.logger == nil {
		return
	}
	attributes := []any{"error", err.Error()}
	if document.ID != "" {
		attributes = append(attributes, "document_id", document.ID, "revision", document.ActiveRevision)
	}
	w.logger.WarnContext(ctx, message, attributes...)
}
