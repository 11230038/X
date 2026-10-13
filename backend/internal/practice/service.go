package practice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The two ways a submission can lose, as things a transaction can report.
//
// Neither is an error the caller ever sees: both mean the transaction has been
// abandoned and the answer is a read of what the winner left behind, so the
// service finishes them outside the transaction it started. They are package
// values rather than a shape on a result because the transaction will not carry
// anything else out of a callback that returns one.
var (
	// errRecorded is the event table's primary key reporting that this request
	// id has been spent. The spender is the same submission — the client gave
	// one id to one submission, and this is that submission arriving twice.
	errRecorded = errors.New("practice: the request id was taken while the submission was being made")
	// errStaleState is the state table's guard reporting that the version read
	// is no longer the stored one. Whoever moved it is a different submission.
	errStaleState = errors.New("practice: the review state moved while the submission was being made")
)

// Service owns the review write path: one submission in, one recorded review
// out.
//
// It is the only place in this domain that writes, and it is here rather than in
// the repository because the decision and the writes it needs are one thing: a
// transaction that spanned less than the whole decision would be able to leave a
// review recorded with no state to show for it.
type Service struct {
	repository Repository
	judge      *Judge
	// now is the service's own clock, and it is a field so that a case can say
	// when a review happened. A due time is the answer to a review, and a case
	// that has to derive it from a clock read outside is a case that tests the
	// clock.
	now func() time.Time
}

// NewService returns the service over storage and a judge.
func NewService(repository Repository, judge *Judge) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("practice: a review service needs a repository")
	}
	if judge == nil {
		return nil, fmt.Errorf("practice: a review service needs a judge")
	}
	return &Service{
		repository: repository,
		judge:      judge,
		now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

// Review records one submission and returns what it produced.
//
// Once a submission is recorded, the same request id submits it again and gets
// the same answer back — not a second review, and not a conflict. That is the
// whole point of the id: a client that did not see a response, or saw two, can
// ask again without asking for something new.
//
// Two mechanisms keep that true and neither stands in for the other: the event
// table's primary key answers "is this the same submission", and the state
// table's version answers "is this a new one". One without the other would give
// away either the retry or the double tap.
func (s *Service) Review(ctx context.Context, review Review) (ReviewResult, error) {
	if err := review.Validate(); err != nil {
		return ReviewResult{}, err
	}
	now := s.now().UTC()

	var result ReviewResult
	err := s.repository.Within(ctx, func(repository Repository) error {
		produced, err := s.record(ctx, repository, review, now)
		if err != nil {
			return err
		}
		result = produced
		return nil
	})

	switch {
	case errors.Is(err, errRecorded):
		return s.replay(ctx, review)
	case errors.Is(err, errStaleState):
		return ReviewResult{}, s.stale(ctx, review)
	case err != nil:
		return ReviewResult{}, err
	}
	return result, nil
}

// record is everything one submission does, and it is written to be rolled back
// as a whole or not at all.
//
// The order of the two collisions is a decision. The event is written before the
// state, because the two collisions mean different things and the one that means
// "the same submission twice" has to be found first: with the state written
// first, the second of two identical submissions would fail the guard and be
// told its version was stale — an answer about a race between two different
// submissions, given to a client that made one submission twice, and one that no
// retry could act on.
func (s *Service) record(ctx context.Context, repository Repository, review Review, now time.Time) (ReviewResult, error) {
	// A submission already recorded is answered from the record and nothing
	// else: not judged again, not scheduled again, not counted again.
	recorded, err := repository.RecordedReview(ctx, review.RequestID)
	if err == nil {
		return replay(review, recorded)
	}
	if !errors.Is(err, ErrNotFound) {
		return ReviewResult{}, err
	}

	entry, err := repository.Entry(ctx, review.QuestionID)
	if err != nil {
		return ReviewResult{}, err
	}
	// The state read comes before the judgement, and only its order in Entry
	// matters as much: everything below reads values that the guard at the end
	// has to still be about.
	state := State{}
	version := int64(0)
	nextVersion := int64(0)
	if entry.State != nil {
		state = entry.State.State
		version = entry.State.Version
		nextVersion = version + 1
	}

	judgement, err := s.judgeAnswer(ctx, entry, review)
	if err != nil {
		return ReviewResult{}, err
	}

	// What the curve does with this review. It is computed before anything is
	// written because the response carries it: the state the surface is shown is
	// the state to store, and a due time derived twice from two readings of the
	// clock is two due times.
	outcome := Outcome{Marker: review.Marker, Rating: review.Rating}
	if judgement != nil {
		outcome.Correct = judgement.IsCorrect
	}
	next, err := Next(state, outcome, now)
	if err != nil {
		return ReviewResult{}, err
	}

	// The version the client read outranks the one just read. Falling back to
	// the stored version would turn a client that is behind into one that is up
	// to date, and the guard would then be guarding the server's state against
	// itself.
	expected := version
	if review.Version != nil {
		expected = *review.Version
	}

	face, reported := answerFace(review, entry, judgement)
	result := ReviewResult{
		RequestID:  review.RequestID,
		QuestionID: review.QuestionID,
		Judgment:   judgement,
		State:      stateOnTheWire(StoredState{State: next, Version: nextVersion}),
		Entry:      reported,
	}
	outcomeJSON, err := json.Marshal(result)
	if err != nil {
		return ReviewResult{}, fmt.Errorf("encode the review of %s: %w", review.RequestID, err)
	}

	// The record first, so that the same submission arriving twice is turned
	// away by the primary key rather than by the guard.
	err = repository.RecordReview(ctx, StoredReview{
		RequestID:         review.RequestID,
		NotebookEntriesID: entry.ID,
		UserAnswer:        review.UserAnswer,
		Rating:            review.Rating,
		Outcome:           outcomeJSON,
		ReviewTime:        now,
	})
	if errors.Is(err, ErrDuplicate) {
		return ReviewResult{}, errRecorded
	}
	if err != nil {
		return ReviewResult{}, err
	}

	switch {
	case entry.State != nil:
		moved, err := repository.AdvanceState(ctx, entry.ID, expected, next)
		if err != nil {
			return ReviewResult{}, err
		}
		if !moved {
			return ReviewResult{}, errStaleState
		}
	case expected != 0:
		// There is no state row, and the client says there is one at a version
		// it has seen. Both cannot be true, and creating the row would write a
		// version this client never read.
		return ReviewResult{}, errStaleState
	default:
		if err := repository.StartState(ctx, entry.ID, next); errors.Is(err, ErrDuplicate) {
			// The row was created between the read and this write: the client
			// did not see it, so what it read is behind, not wrong.
			return ReviewResult{}, errStaleState
		} else if err != nil {
			return ReviewResult{}, err
		}
	}

	// Last, and inside the same transaction: the answer face moves only for a
	// submission that is being recorded for the first time and has a state row
	// to show for it.
	if review.Marker != MarkerSkipped {
		if err := repository.RecordAnswer(ctx, entry.ID, face, now); err != nil {
			return ReviewResult{}, err
		}
	}
	return result, nil
}

// replay answers a submission that has already been recorded.
func replay(review Review, recorded StoredReview) (ReviewResult, error) {
	var result ReviewResult
	if err := json.Unmarshal(recorded.Outcome, &result); err != nil {
		return ReviewResult{}, fmt.Errorf("%w: the review recorded under %s cannot be read: %v",
			ErrInvalidRequest, review.RequestID, err)
	}
	field, err := difference(review, result, recorded)
	if err != nil {
		return ReviewResult{}, err
	}
	if field != "" {
		return ReviewResult{}, fmt.Errorf("%w: %s was recorded with a different %s",
			ErrConflictingRequestID, review.RequestID, field)
	}
	// The recorded response is the answer, which is the point of storing it: the
	// one thing a client is told that a first submission is not is that this
	// submission had been made before.
	result.Replayed = true
	return result, nil
}

// difference names the part of a recorded submission this one does not agree
// with, or returns the empty string when they are the same submission.
//
// The four parts are the whole of what a client sends that can change what
// happens. §2.6 names three of them and leaves out the rating; it is a column of
// this table and it moves the curve, so a retry that rates the same answer
// differently is asking for a different schedule, and the recorded one is not an
// answer to it. The marker is not a column of the table, so it is read back out
// of the response the submission stored — which is why the response has to
// record the marker at all, and why it records it in the answer face's label
// rather than in the judgement: a label read back is the same label, while a
// judgement read back would be an opinion that has to be taken again.
func difference(review Review, result ReviewResult, recorded StoredReview) (string, error) {
	if result.QuestionID != review.QuestionID {
		return "question_id", nil
	}
	if recorded.UserAnswer != review.UserAnswer {
		return "user_answer", nil
	}
	if !sameRating(recorded.Rating, review.Rating) {
		return "rating", nil
	}
	marker, err := result.marker()
	if err != nil {
		return "", err
	}
	if marker != review.Marker {
		return "marker", nil
	}
	return "", nil
}

// replay finishes a submission whose request id was taken while it was being
// made. The read is taken after the transaction that failed has been rolled
// back, so it sees what the writer that got there first committed — a read
// inside it could be answered from before that commit, and would then find no
// row at all and report a collision it cannot describe.
func (s *Service) replay(ctx context.Context, review Review) (ReviewResult, error) {
	recorded, err := s.repository.RecordedReview(ctx, review.RequestID)
	if err != nil {
		return ReviewResult{}, fmt.Errorf("read the review that took request id %s: %w",
			review.RequestID, err)
	}
	return replay(review, recorded)
}

// stale is the answer to a submission that lost the version guard: the state as
// it stands, which is what the client needs in order to decide again.
//
// The read is taken after the rollback for the same reason the replay's is, and
// it is the whole point of carrying the state on the error — a client told only
// that it is behind has to fetch the state and try again, which is two more
// round trips to be told the same thing. When the state cannot be read, the
// conflict is still true and is reported without it: a version invented to fill
// the hole would be worse than a bare conflict.
func (s *Service) stale(ctx context.Context, review Review) error {
	entry, err := s.repository.Entry(ctx, review.QuestionID)
	if err != nil || entry.State == nil {
		return ErrStaleReview
	}
	return &StaleReview{State: stateOnTheWire(*entry.State)}
}

// judgeAnswer judges the answer, unless the user declined to have it judged.
//
// Nothing is judged for a skip, and that is not an optimisation: the user
// answered the question with "not now", and a verdict on it would be a verdict
// on an answer that was not given.
func (s *Service) judgeAnswer(ctx context.Context, entry StoredEntry, review Review) (*Judgment, error) {
	if review.Marker == MarkerSkipped {
		return nil, nil
	}
	judged, err := s.judge.Judge(ctx, entry.Question, review.UserAnswer)
	if err != nil {
		return nil, err
	}
	return &judged, nil
}

// answerFace is what one submission writes to the answer face and what it is
// told about it. The two are computed together because they are one decision
// written twice, and apart because they are not the same value: see AnswerFace.
func answerFace(review Review, entry StoredEntry, judgement *Judgment) (AnswerFace, AnswerState) {
	if judgement == nil {
		// A skip writes nothing and is answered with the face the row already
		// carries: the submission is a decision not to answer, and what the
		// question says about itself is not part of it.
		return AnswerFace{}, answerOnTheWire(entry.Answer)
	}

	face := AnswerFace{
		UserAnswer:   review.UserAnswer,
		IsCorrect:    judgement.IsCorrect,
		Result:       resultOf(review.Marker, judgement.IsCorrect),
		AttemptCount: entry.Answer.AttemptCount + 1,
	}
	switch review.Marker {
	case MarkerMastered:
		resolved := true
		face.Resolved = &resolved
	case MarkerNeedsReview:
		resolved := false
		face.Resolved = &resolved
	}

	reported := AnswerState{
		IsCorrect:    &face.IsCorrect,
		AttemptCount: face.AttemptCount,
		Result:       face.Result,
		Resolved:     face.Resolved,
	}
	if reported.Resolved == nil {
		// This submission answered the question and said nothing about whether
		// it is settled, so the surface is told what the column says. Reporting
		// nil here would say the question is unmarked.
		reported.Resolved = entry.Answer.Resolved
	}
	return face, reported
}
