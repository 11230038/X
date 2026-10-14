package notebook

// The batch import of questions (QN-05, §2.13).
//
// One file, two requests. `dry_run` reports what would happen and writes
// nothing; the same file without the flag does what the report said. The two are
// one implementation and not two, because a preview that classified lines by its
// own rules would be a preview that lies — the report and the commit are the
// same walk over the same file, and the only difference is whether the walk is
// allowed to write. That is the one technical requirement this section has, and
// it is why the walk below is one loop with one boolean in it.
//
// The file is JSON lines: one question per line, in the shape save_question's
// arguments have. Reading a line is import_line.go; CSV is not offered, because
// it would bring a set of quoting and encoding questions this build has no reader
// for, and nothing asks for one.
//
// A conflict here is the same conflict the bank already knows how to talk about,
// because the import is a second door onto the same question and not a second
// identity for it (V3 §2.2). Three things can happen to a line, and the report
// says which:
//
//   - The bank holds this content address with this content: it is the question
//     already there, and there is nothing to write. Importing the same file twice
//     is this case, every line of it.
//   - The bank holds this content address with another face: it is the same
//     question written differently, and the question face is overwritten — which
//     is what an upsert means in this bank. The report names the question
//     beforehand, because an overwrite nobody was told about is the silent
//     failure QN-05 exists to prevent.
//   - The bank holds this question text under another type: one question to a
//     reader, two questions to this bank (the type takes part in the address).
//     It is reported and not written. Writing it would put one sentence in the
//     bank twice, and the second copy is what a reader would meet as a
//     duplicate.
//
// A line is also refused for being unusable at all — no answer, no options on a
// choice question, an answer naming an option that is not offered — and those
// refusals are the ones the judge would make at answer time. A question the judge
// cannot answer is a row the user meets as a broken card, and this is the last
// door at which it can still be turned away.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ImportOutcome is what the import decided about one line of the file.
type ImportOutcome string

const (
	// ImportWritable is a line the commit writes. A preview reports it as
	// writable too: the report is about what the bank would hold afterwards, not
	// about which of the two requests is running.
	ImportWritable ImportOutcome = "writable"
	// ImportMissingAnswer is a line with no answer to judge against. It is its
	// own outcome rather than a kind of invalid because it is the mistake a file
	// is most likely to make and the one a reader can fix fastest.
	ImportMissingAnswer ImportOutcome = "missing_answer"
	// ImportDuplicate is a question the bank already answers, exactly or in
	// another form. ImportConflict says which of the two.
	ImportDuplicate ImportOutcome = "duplicate"
	// ImportInvalid is a line that is not a question this bank can store.
	ImportInvalid ImportOutcome = "invalid"
)

// ImportConflict names the question a line met, and how it met it.
type ImportConflict string

const (
	// ImportConflictNone is a line that met nothing: a new question.
	ImportConflictNone ImportConflict = ""
	// ImportConflictSameQuestion is the bank holding this exact question. The
	// content address is the same and so is everything behind it, so the write
	// would store what is already stored.
	ImportConflictSameQuestion ImportConflict = "same_question"
	// ImportConflictSameQuestionDifferentContent is the same content address with
	// another face. The commit overwrites the question face; what it does not
	// touch is the answer face, which belongs to the review path and not to
	// whoever wrote the question last (this package's opening rule).
	ImportConflictSameQuestionDifferentContent ImportConflict = "same_question_different_content"
	// ImportConflictSameTextOtherType is the same question text stored under
	// another type: two questions by this bank's identity, one question to a
	// reader. The commit refuses the line rather than answer the same sentence
	// twice.
	ImportConflictSameTextOtherType ImportConflict = "same_text_other_type"
)

// ImportEntry is one line of the file and what the import decided about it.
//
// Every field is written for every line, the empty ones included: a report a
// client has to read by guessing which fields are absent is a report that gets
// read wrong exactly when something is wrong.
type ImportEntry struct {
	// Line is the line's number in the file, counted from one. It counts physical
	// lines, the blank ones included, so that the number is one a text editor
	// agrees with.
	Line int
	// Outcome is what will happen to this line.
	Outcome ImportOutcome
	// QuestionID is the content address the line's own text and type have. It is
	// filled in for the two outcomes that are a question this bank takes — writable
	// and duplicate — and left empty for the lines that never got that far, because
	// an address computed from a question the bank would refuse names the row of a
	// different question.
	//
	// A duplicate keeps its own address rather than the bank's, and the two are the
	// same address for an exact duplicate and different for the same text under
	// another type. The question in the way is named by ConflictingQuestionID, so
	// this field always means "the address this line resolves to" and never has to
	// be read together with the outcome to be understood.
	QuestionID string
	// Conflict is which question the line met, and how.
	Conflict ImportConflict
	// ConflictingQuestionID names the question that was met, and ConflictingLine
	// says which earlier line of this same file put it there — zero when it was
	// already in the bank before this request. The line number is what makes a file
	// that contradicts itself fixable: the question id is the same string in both
	// places, and only the number says where to look.
	ConflictingQuestionID string
	ConflictingLine       int
	// Reason says why the line will not be written. It is empty for a writable
	// line, and it is the one field that is a sentence rather than a name: the
	// outcome says what happened, and this says what to do about it.
	Reason string
}

// ImportReport is the answer to both requests: the file line by line, and how
// many lines of each kind there were.
type ImportReport struct {
	// DryRun is the request this report answers, echoed so that a client which
	// sent the same file twice knows which answer it is holding.
	DryRun bool
	// Lines is how many lines the file held, the blank ones excluded: a file that
	// ends in a newline holds the lines that are there, not one more.
	Lines int
	// Parsed is how many of them could be read as a question at all. The rest were
	// not JSON, or not a question this bank can store.
	Parsed int
	// The four counts are the outcomes, one line each, and they add up to Lines.
	Writable      int
	MissingAnswer int
	Duplicate     int
	Invalid       int
	// Entries is the report, in file order. It is empty rather than absent for a
	// file that held nothing.
	Entries []ImportEntry
}

// importWorld is the bank as one request sees it: what storage holds, and the
// lines of this same file that have already been accepted.
//
// The second half is what makes a preview a preview rather than a guess. Two
// lines of one file that carry the same question are a conflict of exactly the
// kind the report exists to expose, and a walk that only asked storage would miss
// it — the first line is not written yet while the second one is being read.
type importWorld struct {
	service  *Service
	ctx      context.Context
	accepted map[string]importSeen
}

// importSeen is a question this request has already accepted, and the line of the
// file it came from.
type importSeen struct {
	question Question
	line     int
}

// Import reads a file of questions and either reports what writing it would do or
// writes it (§2.13).
//
// The file is its whole text and not a reader, because the size of a request is
// the transport's business and the handler is where the transport is: what
// arrives here has already been bounded, and a second bound inside a domain would
// be a second answer to the same question.
//
// A commit that fails partway leaves what it wrote written. That is not a
// transaction it is missing: the file is a sequence of independent questions,
// each of which is one upsert through the same door the tool writes through, and
// pretending the sequence were atomic would be a bigger lie than a partial write
// — a caller who wants to know where it stopped asks for a preview again, which
// will report the lines that got in as the questions the bank holds.
func (s *Service) Import(ctx context.Context, file string, dryRun bool) (ImportReport, error) {
	report := ImportReport{DryRun: dryRun, Entries: []ImportEntry{}}
	world := &importWorld{service: s, ctx: ctx, accepted: map[string]importSeen{}}
	for index, raw := range strings.Split(file, "\n") {
		if strings.TrimSpace(raw) == "" {
			// A blank line is not a question and not a mistake: a file ends in a
			// newline, and a blank line between two questions is a person reading
			// the file. It is not counted, and it does not move the line numbers.
			continue
		}
		report.Lines++
		entry, question := readImportLine(index+1, raw)
		if entry.Outcome == ImportWritable {
			if err := world.classify(&entry, question); err != nil {
				return ImportReport{}, err
			}
			if entry.Outcome == ImportWritable {
				if !dryRun {
					if _, err := s.Save(ctx, question); err != nil {
						return ImportReport{}, err
					}
				}
				// A line the classification left writable joins the world whether
				// or not it was written, because a preview is the commit with the
				// writing left out: the second line of a file that repeats the
				// first has to meet the first one either way.
				world.accepted[question.QuestionID] = importSeen{question: question, line: entry.Line}
			}
		}
		report.count(entry)
		report.Entries = append(report.Entries, entry)
	}
	return report, nil
}

// count adds one line to the tallies. The report is counted as the walk goes
// rather than worked out from the entries at the end, so that the two can only
// disagree by this function being wrong — and it is one switch.
func (r *ImportReport) count(entry ImportEntry) {
	if entry.Outcome != ImportInvalid {
		r.Parsed++
	}
	switch entry.Outcome {
	case ImportWritable:
		r.Writable++
	case ImportMissingAnswer:
		r.MissingAnswer++
	case ImportDuplicate:
		r.Duplicate++
	case ImportInvalid:
		r.Invalid++
	}
}

// classify decides what one writable line meets, and it is the whole of what a
// preview is worth.
//
// It looks at this request's own accepted lines before the bank, which is what
// makes a file that contradicts itself visible: a question written twice in one
// file is the same conflict as a question the bank already holds, and reporting
// it is the difference between a report that describes the file and one that
// describes the bank.
func (w *importWorld) classify(entry *ImportEntry, question Question) error {
	held, line, err := w.questionAt(question.QuestionID)
	switch {
	case err == nil:
		entry.ConflictingQuestionID, entry.ConflictingLine = held.QuestionID, line
		if sameQuestionFace(held, question) {
			entry.Outcome = ImportDuplicate
			entry.Conflict = ImportConflictSameQuestion
			entry.Reason = "the bank already holds this question, content and all"
			return nil
		}
		// The same address is the same question, so this is a rewrite of the
		// question face. It stays writable: refusing it would make a file unable to
		// correct a question it wrote, and the report has said what it will replace.
		entry.Conflict = ImportConflictSameQuestionDifferentContent
		entry.Reason = "the question face stored under this address differs, and will be overwritten"
		return nil
	case !errors.Is(err, ErrNotFound):
		return err
	}
	for _, otherType := range otherQuestionTypes(question.Type) {
		held, line, err := w.questionAt(QuestionID(question.Text, otherType))
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		entry.Outcome = ImportDuplicate
		entry.Conflict = ImportConflictSameTextOtherType
		entry.ConflictingQuestionID, entry.ConflictingLine = held.QuestionID, line
		entry.Reason = fmt.Sprintf(
			"the bank already holds this text as a %s question, and the same text under another type is a second question",
			held.Type)
		return nil
	}
	return nil
}

// questionAt reads the question at one content address: this request's own
// accepted lines first, then the bank.
//
// The line it reports is zero for a question that was in the bank before this
// request, and the file's own line number for one this request has accepted —
// which is the difference between "the bank has this" and "this file said it
// twice".
func (w *importWorld) questionAt(questionID string) (Question, int, error) {
	if seen, ok := w.accepted[questionID]; ok {
		return seen.question, seen.line, nil
	}
	entry, err := w.service.repository.Entry(w.ctx, questionID)
	if err != nil {
		return Question{}, 0, err
	}
	return entry.Question, 0, nil
}

// otherQuestionTypes lists the two kinds a question text could have been stored
// under, when the one being imported is not it. The address is derived from the
// text and the type, so the two are two lookups and not a search over the bank.
func otherQuestionTypes(questionType QuestionType) []QuestionType {
	others := make([]QuestionType, 0, 2)
	for _, candidate := range []QuestionType{QuestionChoice, QuestionFillBlank, QuestionShortAnswer} {
		if candidate != questionType {
			others = append(others, candidate)
		}
	}
	return others
}

// sameQuestionFace reports whether two questions at one content address hold the
// same thing.
//
// The type and the address are the same by construction — the address is derived
// from the text and the type — so what is compared is everything the address does
// not cover: the options, the answer, the explanation, where the question came
// from, and the text as it was actually written, whitespace included. A line that
// differs from the stored question only in the width of a gap does rewrite the
// stored text, and a report that called that "the same" would be wrong about the
// one column a reader looks at first.
func sameQuestionFace(one, other Question) bool {
	if one.Text != other.Text ||
		one.CorrectAnswer != other.CorrectAnswer ||
		one.Explanation != other.Explanation ||
		one.Difficulty != other.Difficulty ||
		one.Source != other.Source ||
		one.MaterialID != other.MaterialID {
		return false
	}
	if len(one.Options) != len(other.Options) {
		return false
	}
	for index, option := range one.Options {
		if option != other.Options[index] {
			return false
		}
	}
	return slices.Equal(one.KnowledgePoints, other.KnowledgePoints)
}
