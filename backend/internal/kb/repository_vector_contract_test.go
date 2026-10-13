package kb

// The index worker's half of the storage contract: which document a claim takes,
// who is allowed to write it back, and what the schema does when the columns
// that describe a claim disagree.
//
// Every case here is about a state only the database can hold — a row another
// worker is holding, a lease that ran out, a check constraint — so every case
// runs against a real seekdb. The fixtures live in repository_db_test.go.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/embed"
)

// vectorDimensions is the width of the embedding column, fixed by the schema.
const vectorDimensions = 1024

// claimTime is the moment the tests below claim at. Whole seconds, because
// vector_claimed_at is a DATETIME.
var claimTime = atSecond(10)

// vectorOf builds a vector whose only non-zero component is the axis given.
//
// Two of them share no direction, so the cosine distance between two of them is
// 1 while a stored one is at distance 0 from its own axis; mixedVectorOf spreads
// a vector over two axes and so sits between the two, which is how the ordering
// test tells a nearest neighbour from a far one without doing the arithmetic.
func vectorOf(axis int) string {
	values := make([]float32, vectorDimensions)
	values[axis] = 1
	return embed.Vector(values).String()
}

func mixedVectorOf(first, second int) string {
	values := make([]float32, vectorDimensions)
	values[first], values[second] = 1, 1
	return embed.Vector(values).String()
}

// pendingDocument stores one file and the document parsed from it, both waiting
// for vectors, in a base the caller has already created.
func pendingDocument(t *testing.T, repo *GORMRepository, resource *data.Resource,
	id string, number int, at time.Time) Document {
	t.Helper()
	fileID := libraryFileID(number)
	addLibraryFile(t, resource, fileID)
	return addDocument(t, repo, dbDocument{
		ID: id, KBID: "kb_a", FileID: fileID, CreatedAt: at,
		Text: "alpha beta", Chunks: []string{"alpha", "beta"},
	})
}

func claimed(t *testing.T, repo *GORMRepository, owner string, now time.Time) Document {
	t.Helper()
	document, err := repo.ClaimDocument(context.Background(), owner, time.Minute, now)
	if err != nil {
		t.Fatalf("claim a document for %s: %v", owner, err)
	}
	return document
}

// claimColumns reads the three columns that describe a claim. The domain type
// does not carry them — nothing in the application reads a claim time — and they
// are what the schema's check constraint is about, so a test about a claim has
// to look at the row.
func claimColumns(t *testing.T, resource *data.Resource, documentID string) (string, *string, *time.Time) {
	t.Helper()
	var row struct {
		State     string     `gorm:"column:vector_state"`
		Owner     *string    `gorm:"column:vector_owner"`
		ClaimedAt *time.Time `gorm:"column:vector_claimed_at"`
	}
	err := resource.GORM().Raw(
		"SELECT vector_state, vector_owner, vector_claimed_at FROM documents WHERE document_id = ?",
		documentID).Scan(&row).Error
	if err != nil {
		t.Fatalf("read the claim columns of %s: %v", documentID, err)
	}
	return row.State, row.Owner, row.ClaimedAt
}

func TestDBAClaimTakesTheOldestDocumentAndTakesItOnce(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	pendingDocument(t, repo, resource, "doc_later", 1, atSecond(5))
	pendingDocument(t, repo, resource, "doc_earlier", 2, atSecond(1))

	first := claimed(t, repo, "worker_1", claimTime)
	if first.ID != "doc_earlier" {
		t.Errorf("the first claim took %s, want the oldest document waiting", first.ID)
	}
	// The claim is the whole of the state the schema requires of a running
	// document: the state, the owner and the moment.
	state, owner, claimedAt := claimColumns(t, resource, first.ID)
	if state != VectorStateRunning || owner == nil || *owner != "worker_1" || claimedAt == nil {
		t.Errorf("the claimed document is %q, owned by %v, claimed at %v",
			state, owner, claimedAt)
	}

	// A second claim by the same worker takes the other document, not the one it
	// already holds.
	second := claimed(t, repo, "worker_1", claimTime)
	if second.ID != "doc_later" {
		t.Errorf("the second claim took %s, want the other document", second.ID)
	}
	// And with both held there is nothing to claim, which is what the worker
	// loop polls on: it has to be an error, not a document it would embed twice.
	if _, err := repo.ClaimDocument(context.Background(), "worker_1", time.Minute, claimTime); !errors.Is(err, ErrNotFound) {
		t.Errorf("claiming with nothing waiting: %v, want %v", err, ErrNotFound)
	}
}

func TestDBTwoWorkersNeverClaimOneDocument(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	for number := 1; number <= 4; number++ {
		pendingDocument(t, repo, resource, "doc_"+string(rune('a'+number-1)), number, atSecond(number))
	}

	// Four workers ask at the same moment. The read that picks a candidate is
	// not what makes a claim exclusive — the write's WHERE clause is, and a
	// claim taken on a row somebody else won is how one document gets embedded
	// twice.
	const workers = 4
	start := make(chan struct{})
	documents := make([]Document, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range documents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			documents[i], errs[i] = repo.ClaimDocument(
				context.Background(), "worker_"+string(rune('a'+i)), time.Minute, claimTime)
		}()
	}
	close(start)
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d claimed nothing: %v", i, err)
			continue
		}
		if seen[documents[i].ID] {
			t.Errorf("document %s was claimed by two workers, want each held once", documents[i].ID)
		}
		seen[documents[i].ID] = true
	}
	if len(seen) != 4 {
		t.Errorf("%d documents were claimed, want one per worker: %v", len(seen), seen)
	}

	// One document per owner, which is the same thing read from the stored side.
	var owners []struct {
		Owner string `gorm:"column:vector_owner"`
		Count int    `gorm:"column:n"`
	}
	err := resource.GORM().Raw(
		"SELECT vector_owner AS owner, COUNT(*) AS n FROM documents GROUP BY vector_owner").
		Scan(&owners).Error
	if err != nil {
		t.Fatalf("count the claims per owner: %v", err)
	}
	for _, row := range owners {
		if row.Count != 1 {
			t.Errorf("owner %q holds %d documents, want 1", row.Owner, row.Count)
		}
	}
}

func TestDBALeaseThatRanOutCanBeTakenOver(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	abandoned := pendingDocument(t, repo, resource, "doc_1", 1, atSecond(1))
	claimed(t, repo, "worker_1", claimTime)

	// Inside the lease the document belongs to the worker that took it, even
	// though that worker has said nothing since.
	if _, err := repo.ClaimDocument(context.Background(), "worker_2", time.Minute,
		claimTime.Add(30*time.Second)); !errors.Is(err, ErrNotFound) {
		t.Errorf("claiming a document inside its lease: %v, want %v", err, ErrNotFound)
	}

	// Past it the document is anybody's to take: the first worker is gone, and a
	// document claimed by a process that died must not wait forever.
	taken := claimed(t, repo, "worker_2", claimTime.Add(2*time.Minute))
	if taken.ID != abandoned.ID {
		t.Errorf("the lease passed to %s, want the document the first worker abandoned (%s)",
			taken.ID, abandoned.ID)
	}
	state, owner, claimedAt := claimColumns(t, resource, abandoned.ID)
	if state != VectorStateRunning || owner == nil || *owner != "worker_2" || claimedAt == nil {
		t.Errorf("after the takeover the document is %q, owned by %v, claimed at %v",
			state, owner, claimedAt)
	}
}

func TestDBOnlyTheOwnerWritesADocumentBack(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))

	ctx := context.Background()
	writes := []struct {
		name  string
		write func(documentID, owner string) error
	}{
		{"complete", func(id, owner string) error { return repo.CompleteDocument(ctx, id, owner) }},
		{"fail", func(id, owner string) error {
			return repo.FailDocument(ctx, id, owner, "the provider refused")
		}},
		{"retry", func(id, owner string) error { return repo.RetryDocument(ctx, id, owner, "stopping") }},
	}
	for number, scenario := range writes {
		// A document of its own per scenario: each of the three writes ends the
		// claim it was made under, and the last one leaves the document finished.
		pendingDocument(t, repo, resource, "doc_"+string(rune('1'+number)), number+1, atSecond(1))
		claim := claimed(t, repo, "worker_1", claimTime)

		// A worker whose claim was taken away — a rebuild cleared it, or the
		// lease ran out and somebody else picked it up — must not be able to
		// write. That is what the owner in the WHERE clause is for: without it,
		// the worker that no longer owns the document could mark it ready with
		// vectors for a revision nobody serves.
		if err := scenario.write(claim.ID, "worker_2"); !errors.Is(err, ErrConflict) {
			t.Errorf("%s by a worker that does not hold the document: %v, want %v",
				scenario.name, err, ErrConflict)
		}
		// And the refusal left nothing behind.
		state, owner, claimedAt := claimColumns(t, resource, claim.ID)
		if state != VectorStateRunning || owner == nil || *owner != "worker_1" || claimedAt == nil {
			t.Errorf("after a refused %s the document is %q, owned by %v", scenario.name, state, owner)
		}

		// The owner writes it back, which is the same call going through.
		if err := scenario.write(claim.ID, "worker_1"); err != nil {
			t.Errorf("%s by the owner: %v", scenario.name, err)
		}
	}
}

func TestDBReasonsSurviveTheStateTheyExplain(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	pendingDocument(t, repo, resource, "doc_1", 1, atSecond(1))
	claim := claimed(t, repo, "worker_1", claimTime)

	// A failure keeps why, so that whoever looks next does not have to guess,
	// and it carries no claim: the constraint refuses a non-running document
	// that still names an owner.
	if err := repo.FailDocument(context.Background(), claim.ID, "worker_1", "the provider refused"); err != nil {
		t.Fatalf("fail the document: %v", err)
	}
	stored, err := repo.Document(context.Background(), claim.ID)
	if err != nil {
		t.Fatalf("read the document back: %v", err)
	}
	if stored.VectorState != VectorStateFailed || stored.VectorError != "the provider refused" {
		t.Errorf("the failed document is %q because %q, want the refusal kept",
			stored.VectorState, stored.VectorError)
	}
	state, owner, claimedAt := claimColumns(t, resource, claim.ID)
	if state != VectorStateFailed || owner != nil || claimedAt != nil {
		t.Errorf("the failed document is %q, owned by %v, claimed at %v, want no claim",
			state, owner, claimedAt)
	}

	// It is out of the queue rather than waiting in it: only a document that is
	// waiting, or one whose lease ran out, is claimable.
	if _, err := repo.ClaimDocument(context.Background(), "worker_2", time.Minute, claimTime); !errors.Is(err, ErrNotFound) {
		t.Errorf("claiming a failed document: %v, want %v", err, ErrNotFound)
	}
}

func TestDBARebuildHandsTheDocumentBackToTheQueue(t *testing.T) {
	repo, resource := newDBRepository(t)
	// writeRevision stores the base these documents go in as well.
	first := writeRevision(t, repo, resource, "doc_1", atSecond(1), "alpha beta", []string{"alpha", "beta"})
	claimed(t, repo, "worker_1", claimTime)

	// A rebuild writes new chunks with no vectors and clears the claim in the
	// same statement. Leaving the row running with a stale owner would be a
	// document the lease hands to a second worker while the first still believes
	// it owns it.
	second := documentWrite(t, dbDocument{
		ID: "doc_1", KBID: "kb_a", FileID: first.FileID, Revision: 2,
		CreatedAt: atSecond(2), Text: "alpha beta gamma", Chunks: []string{"alpha", "beta gamma"},
	})
	if err := repo.ReplaceRevision(context.Background(), second, 1); err != nil {
		t.Fatalf("replace the revision: %v", err)
	}
	state, owner, claimedAt := claimColumns(t, resource, "doc_1")
	if state != VectorStatePending || owner != nil || claimedAt != nil {
		t.Errorf("the rebuilt document is %q, owned by %v, claimed at %v, want it waiting again",
			state, owner, claimedAt)
	}

	// The worker that held it cannot finish it any more, and the document is
	// waiting for whoever asks next.
	if err := repo.CompleteDocument(context.Background(), "doc_1", "worker_1"); !errors.Is(err, ErrConflict) {
		t.Errorf("finishing a document a rebuild took away: %v, want %v", err, ErrConflict)
	}
	if again := claimed(t, repo, "worker_1", claimTime); again.ID != "doc_1" {
		t.Errorf("the rebuild left %s in the queue, want the rebuilt document", again.ID)
	}
}

func TestDBAttachmentsAreNeverClaimed(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	fileID := libraryFileID(1)
	addLibraryFile(t, resource, fileID)
	addDocument(t, repo, dbDocument{
		ID: "doc_attachment", FileID: fileID, CreatedAt: atSecond(1),
		Text: "alpha beta", Chunks: []string{"alpha", "beta"},
	})

	// An attachment is embedded by nobody: it is there for the turns that
	// carried it, and retrieval never reads it. The rule lives in the claim's
	// WHERE clause, so this is where it is checked.
	if _, err := repo.ClaimDocument(context.Background(), "worker_1", time.Minute, claimTime); !errors.Is(err, ErrNotFound) {
		t.Errorf("claiming an attachment: %v, want %v", err, ErrNotFound)
	}
	// It is not claimed, not waiting for a worker, and its chunks are still
	// there for the turns that read them.
	state, owner, _ := claimColumns(t, resource, "doc_attachment")
	if state != VectorStatePending || owner != nil {
		t.Errorf("the attachment is %q, owned by %v, want it untouched", state, owner)
	}
	if waiting, err := repo.PendingChunks(context.Background(), "doc_attachment", 1); err != nil || len(waiting) != 2 {
		t.Errorf("the attachment reads %d chunks waiting (err %v), want its own 2", len(waiting), err)
	}
}

func TestDBTheClaimColumnsCannotDisagree(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	pendingDocument(t, repo, resource, "doc_1", 1, atSecond(1))

	// The schema refuses a document that says it is running without saying who
	// is running it. Without that, a row stuck in 'running' with no claim would
	// be invisible to the lease and never reclaimed — and the repository's own
	// claim and release both lean on the constraint being there.
	incoherent := []struct{ name, query string }{
		{"running without an owner",
			"UPDATE documents SET vector_state = 'running', vector_owner = NULL, vector_claimed_at = NULL WHERE document_id = 'doc_1'"},
		{"running without a claim time",
			"UPDATE documents SET vector_state = 'running', vector_owner = 'worker_1', vector_claimed_at = NULL WHERE document_id = 'doc_1'"},
		{"pending with an owner",
			"UPDATE documents SET vector_state = 'pending', vector_owner = 'worker_1', vector_claimed_at = NULL WHERE document_id = 'doc_1'"},
	}
	for _, scenario := range incoherent {
		if err := testdb.Try(resource, scenario.query); err == nil {
			t.Errorf("%s was accepted, want the schema to refuse it", scenario.name)
		}
	}

	// The coherent version of the first one goes through, so what refused the
	// others is the constraint and not the statement.
	err := testdb.Try(resource, "UPDATE documents SET vector_state = 'running', "+
		"vector_owner = 'worker_1', vector_claimed_at = NOW() WHERE document_id = 'doc_1'")
	if err != nil {
		t.Fatalf("a coherent claim was refused: %v", err)
	}
}

func TestDBPendingChunksAreTheOnesWithoutVectors(t *testing.T) {
	repo, resource := newDBRepository(t)
	// writeRevision stores the base as well.
	writeRevision(t, repo, resource, "doc_1", atSecond(1), "alpha beta", []string{"alpha", "beta"})

	chunks, err := repo.PendingChunks(context.Background(), "doc_1", 1)
	if err != nil {
		t.Fatalf("read the chunks waiting for vectors: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("%d chunks are waiting, want the revision's 2", len(chunks))
	}
	if got, want := chunkIDs(chunks), chunkIDsOf(t, repo, "doc_1", 1); !slices.Equal(got, want) {
		t.Errorf("the pending chunks are %v, want the revision's own in the same order: %v", got, want)
	}

	// One vector, written through the same call the worker uses.
	chunks[0].Embedding = vectorOf(0)
	if err := repo.SaveEmbeddings(context.Background(), chunks[:1]); err != nil {
		t.Fatalf("store a vector: %v", err)
	}
	waiting, err := repo.PendingChunks(context.Background(), "doc_1", 1)
	if err != nil {
		t.Fatalf("read the chunks waiting for vectors: %v", err)
	}
	if len(waiting) != 1 || waiting[0].ChunkID != chunks[1].ChunkID {
		t.Errorf("the waiting chunks are %v, want only the one with no vector", chunkIDs(waiting))
	}

	// A revision that has no chunks of its own has nothing waiting, which is
	// what keeps a worker from embedding an idle revision's text again.
	for _, revision := range []int{2, 3} {
		if waiting, err := repo.PendingChunks(context.Background(), "doc_1", revision); err != nil || len(waiting) != 0 {
			t.Errorf("revision %d has %d chunks waiting (err %v), want none", revision, len(waiting), err)
		}
	}
}

// chunkIDs is what a list of chunk records is, by id.
func chunkIDs(chunks []ChunkRecord) []string {
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		ids = append(ids, chunk.ChunkID)
	}
	return ids
}
