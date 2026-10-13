package kb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"backend/internal/embed"
)

// providerError is a failure from the provider, which is the only kind of
// failure that says anything about whether trying again could work.
func providerError(status int) error {
	return &embed.HTTPError{StatusCode: status, Message: "the provider said no"}
}

// fill indexes one document in one pass, asserting the pass found work and
// finished it.
func (h *harness) fill(t *testing.T, worker *Worker) {
	t.Helper()
	worked, err := worker.IndexOne(context.Background())
	if err != nil {
		t.Fatalf("IndexOne() error = %v", err)
	}
	if !worked {
		t.Fatal("the worker found no document to index")
	}
}

func TestWorkerEmbedsEveryChunkAndMarksTheDocumentReady(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)

	h.fill(t, h.worker(t))

	chunks := h.repo.activeChunks(document.ID)
	if len(chunks) < 2 {
		t.Fatalf("the fixture produced %d chunks, want at least 2", len(chunks))
	}
	for _, chunk := range chunks {
		// The vector is the one the provider returned for this chunk's text, in
		// the order the texts were asked for. A worker that stored the group's
		// vectors against the wrong chunks would pass a "not empty" check and be
		// wrong for the life of the revision.
		want := derivedVector(chunk.Content).String()
		if got := h.repo.embeddingOf(chunk.ChunkID); got != want {
			t.Errorf("chunk %s has vector %s, want %s", chunk.ChunkID, got, want)
		}
	}
	if state := h.stored(t, document.ID).VectorState; state != VectorStateReady {
		t.Errorf("the document's vector state is %q, want %q", state, VectorStateReady)
	}
	// Everything is embedded, so there is nothing left to do.
	if worked, err := h.worker(t).IndexOne(context.Background()); worked || err != nil {
		t.Errorf("a second pass reported work = %v, error = %v", worked, err)
	}
}

// An attachment is a document that belongs to no base, and the rule that it is
// never embedded lives in the claim rather than in a state of its own (§0.3 第 4
// 条 f).
func TestWorkerLeavesAttachmentsAlone(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	attachment := h.addDocument(t, "", "lib_attachment", sampleText)
	document := h.addDocument(t, base.ID, "lib_one", sampleText)

	worker := h.worker(t)
	h.fill(t, worker)

	if state := h.stored(t, attachment.ID).VectorState; state != VectorStatePending {
		t.Errorf("the attachment's vector state is %q, want it left alone", state)
	}
	for _, chunk := range h.repo.activeChunks(attachment.ID) {
		if h.repo.embeddingOf(chunk.ChunkID) != "" {
			t.Errorf("chunk %s of an attachment has a vector", chunk.ChunkID)
		}
	}
	if state := h.stored(t, document.ID).VectorState; state != VectorStateReady {
		t.Errorf("the document in the base is %q, want %q", state, VectorStateReady)
	}
	// The base's document is indexed, so the next pass has nothing left: the
	// attachment is not work.
	if worked, err := worker.IndexOne(context.Background()); worked || err != nil {
		t.Errorf("the worker found work = %v, error = %v, want the attachment left alone", worked, err)
	}
}

// The claim is what keeps one document from being embedded by two workers at
// once, and what makes that true is the update refusing a row somebody else has
// already taken.
func TestOnlyOneWorkerClaimsADocument(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	h.addDocument(t, base.ID, "lib_one", sampleText)

	const racers = 8
	now := time.Now().UTC()
	wins := make([]bool, racers)
	var wait sync.WaitGroup
	for index := 0; index < racers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := h.repo.ClaimDocument(context.Background(),
				fmt.Sprintf("worker-%d", index), time.Minute, now)
			wins[index] = err == nil
		}(index)
	}
	wait.Wait()

	won := 0
	for _, win := range wins {
		if win {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d of %d workers claimed the same document, want 1", won, racers)
	}
}

// A worker that dies mid-document leaves the row claimed. The lease is what
// makes that a delay rather than a document nobody will ever index.
func TestAStaleClaimCanBeTakenOver(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	now := time.Now().UTC()

	if _, err := h.repo.ClaimDocument(context.Background(), "first", 10*time.Second, now); err != nil {
		t.Fatalf("the first claim failed: %v", err)
	}
	// Inside the lease the document is the first worker's, and asking for it is
	// not an error — it is simply not on offer.
	if _, err := h.repo.ClaimDocument(context.Background(), "second", 10*time.Second, now.Add(5*time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a live claim was handed out again: error = %v, want ErrNotFound", err)
	}
	// Past it, the next worker takes over.
	if _, err := h.repo.ClaimDocument(context.Background(), "second", 10*time.Second, now.Add(11*time.Second)); err != nil {
		t.Fatalf("an expired claim was not taken over: %v", err)
	}
	// And the worker that owned it cannot write to it any more: its vectors are
	// for a document somebody else is now indexing.
	if err := h.repo.CompleteDocument(context.Background(), document.ID, "first"); !errors.Is(err, ErrConflict) {
		t.Errorf("the first worker finished a document it no longer held: error = %v, want ErrConflict", err)
	}
}

// A provider that is down is waited out rather than abandoned: the document
// goes back to the queue with the reason kept, and the next pass indexes it.
// §2.3's "embedding 服务整晚不可用" must not cost a permanent hole in the index.
func TestWorkerWaitsOutAProviderThatIsUnwell(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	h.embedder.errs = []error{providerError(503)}

	worker := h.worker(t)
	worked, err := worker.IndexOne(context.Background())
	if !worked || err == nil {
		t.Fatalf("a failed pass reported work = %v, error = %v", worked, err)
	}
	failed := h.stored(t, document.ID)
	if failed.VectorState != VectorStatePending {
		t.Errorf("the document is %q after a transient failure, want it back in the queue", failed.VectorState)
	}
	if failed.VectorError == "" {
		t.Error("the document does not say why it has no vectors")
	}
	for _, chunk := range h.repo.activeChunks(document.ID) {
		if h.repo.embeddingOf(chunk.ChunkID) != "" {
			t.Errorf("chunk %s was stored although the provider answered nothing", chunk.ChunkID)
		}
	}

	h.fill(t, worker)
	recovered := h.stored(t, document.ID)
	if recovered.VectorState != VectorStateReady {
		t.Errorf("the document is %q after the provider came back, want %q", recovered.VectorState, VectorStateReady)
	}
	if recovered.VectorError != "" {
		t.Errorf("the document still carries %q after a successful pass", recovered.VectorError)
	}
}

// A refusal is not a hiccup: asking the same question again gets the same
// answer, so the document is parked until a rebuild asks afresh — which is also
// how a base moves onto another embedding model.
func TestWorkerParksADocumentTheProviderRefused(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	h.embedder.errs = []error{providerError(400), nil}

	worker := h.worker(t)
	if _, err := worker.IndexOne(context.Background()); err == nil {
		t.Fatal("a refused document reported no failure")
	}
	parked := h.stored(t, document.ID)
	if parked.VectorState != VectorStateFailed {
		t.Errorf("the document is %q, want %q", parked.VectorState, VectorStateFailed)
	}
	if !strings.Contains(parked.VectorError, "400") {
		t.Errorf("the document says %q, want the provider's refusal", parked.VectorError)
	}
	// It is not offered again: the provider's answer would be the same.
	if worked, err := worker.IndexOne(context.Background()); worked || err != nil {
		t.Errorf("a parked document was picked up again: work = %v, error = %v", worked, err)
	}

	if _, err := h.service.RebuildDocument(context.Background(), document.ID); err != nil {
		t.Fatalf("RebuildDocument() error = %v", err)
	}
	h.fill(t, worker)
	rebuilt := h.stored(t, document.ID)
	if rebuilt.VectorState != VectorStateReady || rebuilt.VectorError != "" {
		t.Errorf("after a rebuild the document is %q / %q, want it indexed",
			rebuilt.VectorState, rebuilt.VectorError)
	}
}

// Work already paid for is not paid for twice: the vectors of the groups that
// finished stay, and a later pass asks only for what is missing.
func TestWorkerKeepsTheGroupsItAlreadyStored(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	chunks := h.repo.activeChunks(document.ID)
	if len(chunks) < 3 {
		t.Fatalf("the fixture produced %d chunks, want at least 3", len(chunks))
	}
	// One chunk per call, the second of which fails.
	h.embedder.errs = []error{nil, providerError(500)}

	worker := h.worker(t, func(options *WorkerOptions) { options.EmbedBatchSize = 1 })
	if _, err := worker.IndexOne(context.Background()); err == nil {
		t.Fatal("the failed second group reported no failure")
	}
	if h.embedder.calls != 2 {
		t.Errorf("the worker made %d provider calls, want one per group", h.embedder.calls)
	}
	stored := 0
	for _, chunk := range h.repo.activeChunks(document.ID) {
		if h.repo.embeddingOf(chunk.ChunkID) != "" {
			stored++
		}
	}
	if stored != 1 {
		t.Errorf("%d chunks have vectors, want the one group that finished", stored)
	}

	// The next pass asks for the rest, and does not ask for what it has.
	h.embedder.texts = nil
	h.fill(t, worker)
	if len(h.embedder.texts) != len(chunks)-1 {
		t.Errorf("the second pass asked for %d texts, want the %d still missing",
			len(h.embedder.texts), len(chunks)-1)
	}
	if state := h.stored(t, document.ID).VectorState; state != VectorStateReady {
		t.Errorf("the document is %q after the second pass, want %q", state, VectorStateReady)
	}
}

// Stopping is not failing. The caller's decision ends the pass after the group
// in flight, without another request and without the document being marked as
// something the provider refused.
func TestWorkerStopsWithoutAskingForMore(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.embedder.onCall = func(int) { cancel() }

	worker := h.worker(t, func(options *WorkerOptions) { options.EmbedBatchSize = 1 })
	worked, err := worker.IndexOne(ctx)
	if !worked || err == nil {
		t.Fatalf("a stopped pass reported work = %v, error = %v", worked, err)
	}
	if h.embedder.calls != 1 {
		t.Errorf("the worker made %d provider calls after the caller stopped, want 1", h.embedder.calls)
	}
	stopped := h.stored(t, document.ID)
	if stopped.VectorState != VectorStatePending {
		t.Errorf("the document is %q after a stop, want it back in the queue", stopped.VectorState)
	}
	if stopped.VectorError != "" {
		t.Errorf("the document blames the provider for the caller's decision: %q", stopped.VectorError)
	}

	// A worker asked to stop before it starts does not even claim, so nothing is
	// left for the lease to clean up.
	stoppedCtx, cancelStopped := context.WithCancel(context.Background())
	cancelStopped()
	before := h.embedder.calls
	if worked, err := worker.IndexOne(stoppedCtx); worked || err != nil {
		t.Errorf("a stopped worker did work = %v, error = %v", worked, err)
	}
	if h.embedder.calls != before {
		t.Errorf("a stopped worker made %d provider calls", h.embedder.calls-before)
	}
}

func TestWorkerRefusesIncompleteWiring(t *testing.T) {
	valid := WorkerOptions{Lease: time.Second, Workers: 1, EmbedBatchSize: 1}
	repository := newFakeRepository()
	embedder := &stubEmbedder{vector: embed.Vector{1}}

	cases := map[string]struct {
		options WorkerOptions
		repo    Repository
		embed   embed.Embedder
	}{
		"no repository": {valid, nil, embedder},
		"no embedder":   {valid, repository, nil},
		"no lease":      {WorkerOptions{Workers: 1, EmbedBatchSize: 1}, repository, embedder},
		"no workers":    {WorkerOptions{Lease: time.Second, EmbedBatchSize: 1}, repository, embedder},
		"no batch":      {WorkerOptions{Lease: time.Second, Workers: 1}, repository, embedder},
	}
	for name, test := range cases {
		if _, err := NewWorker(test.options, test.repo, test.embed); err == nil {
			t.Errorf("%s: NewWorker() = nil error, want a refusal", name)
		}
	}
	if _, err := NewWorker(valid, repository, embedder); err != nil {
		t.Errorf("NewWorker(complete) error = %v", err)
	}
}

// The reason is written into a 128-character column, so a long message is cut
// here rather than making the write fail and leaving the document claimed.
func TestAProviderMessageIsCutToFitTheColumn(t *testing.T) {
	long := providerError(400)
	long.(*embed.HTTPError).Message = strings.Repeat("很长的一条错误信息", 40)

	message := errorMessage(long)
	if utf8.RuneCountInString(message) != errorMessageChars {
		t.Errorf("the message is %d characters, want %d", utf8.RuneCountInString(message), errorMessageChars)
	}
	if !strings.HasSuffix(message, "…") {
		t.Error("a cut message does not say it was cut")
	}
	if short := errorMessage(errors.New("简短的原因")); short != "简短的原因" {
		t.Errorf("a short message was changed: %q", short)
	}
}
