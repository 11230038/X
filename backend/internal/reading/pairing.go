package reading

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"backend/internal/authz"
)

// The bilingual pairing of two materials (READ-06).
//
// §2.10 turns the requirement into three decisions and this file is all three:
// the recommendation is a query that stores nothing, the confirmation is one row,
// and every downstream behaviour asks that row before it does anything. The third
// is the one with teeth — "quietly rendered nothing" is the failure mode the
// requirement names, so the answer to a question about a material that is not
// paired is a state with a reason, never an empty result. That is the shape an
// extension's availability already has (§2.9), and it is deliberately the same.
//
// What this file does not do is derive anything. No bilingual text is produced,
// stored or queued: "generating the derived EPUB belongs to a later capability"
// is read here as permission to stop at the confirmation.

const (
	pairingIDPrefix = "rp_"

	// pairingCandidateLimit is how many suggestions one read may return. The list
	// is ordered by the score below, and a reader who has to scroll past ten
	// suggestions has been given a catalogue rather than a recommendation.
	pairingCandidateLimit = 10

	// The two reasons a material can be not-enabled for. They are sentences
	// because a state without one is a state nobody can act on, and they are two
	// because the reader's next step differs: one is "confirm a counterpart", the
	// other is "this material has no text to compare".
	pairingReasonNotPaired  = "no bilingual pairing has been confirmed for this material"
	pairingReasonNoLanguage = "the language of this material could not be read from its text, so no counterpart was looked for"
)

// Candidate is one material the catalogue offers as a counterpart.
//
// Nothing about it is stored: the whole list is computed per request and thrown
// away, which is what makes a row in `reading_pairings` mean "the reader said so"
// and nothing weaker (§2.10).
type Candidate struct {
	MaterialID string
	Filename   string
	Language   string
	TextChars  int64
	// Score is why this one is on the list and where in it: the words the two
	// filenames share and how close the two texts are in length, in that order of
	// weight. It is shown so that a reader can disagree with the order rather than
	// only with the list.
	Score float64
}

// Pairing is one confirmed pairing: two materials in two languages that a reader
// has said are the same book.
//
// The two ends are named rather than implied because the row is one row and both
// materials are in it, and the languages are stored rather than derived because
// they are what the reader confirmed — a text that changes language later (a
// document re-parsed into another revision) must not rewrite what was agreed.
type Pairing struct {
	PairingID string
	// PrimaryMaterialID is the material the confirmation was made from. Which end
	// is "primary" is not a property of the pair, so a caller that asks about one
	// material is handed the row with that material on the primary side
	// (reorientedTo).
	PrimaryMaterialID     string
	CounterpartMaterialID string
	PrimaryLanguage       string
	CounterpartLanguage   string
	CreatedAt             time.Time
}

// Other returns the material on the far side of the pairing from one end, with
// the language that side is in. A material that is not in the pair at all gets
// the counterpart side, which is the only answer left and is never asked for.
func (p Pairing) Other(materialID string) (string, string) {
	if materialID == p.CounterpartMaterialID {
		return p.PrimaryMaterialID, p.PrimaryLanguage
	}
	return p.CounterpartMaterialID, p.CounterpartLanguage
}

// reorientedTo returns the pairing seen from one end of it.
//
// A pairing is a relationship and has no natural direction, but a question about
// one material does: "what is the other one" is only answerable once the asker
// knows which end it is holding. Doing it here means no caller has to compare ids
// to find out.
func (p Pairing) reorientedTo(materialID string) Pairing {
	if p.PrimaryMaterialID == materialID || p.CounterpartMaterialID != materialID {
		return p
	}
	return Pairing{
		PairingID:             p.PairingID,
		PrimaryMaterialID:     p.CounterpartMaterialID,
		CounterpartMaterialID: p.PrimaryMaterialID,
		PrimaryLanguage:       p.CounterpartLanguage,
		CounterpartLanguage:   p.PrimaryLanguage,
		CreatedAt:             p.CreatedAt,
	}
}

// PairingRequest is one confirmation a reader makes.
//
// The material being paired is the route's and the counterpart is the body's,
// which is the same split a mark's material has: the route names the thing the
// action is about, and the body says what to do with it.
type PairingRequest struct {
	MaterialID            string
	CounterpartMaterialID string
}

// Validate checks the two ids before anything is read.
//
// The two checks are the two the database would make anyway — a blank id and a
// material paired with itself — and they are made here so that the refusal is a
// sentence about the request rather than a constraint violation about a row.
func (r PairingRequest) Validate() error {
	if strings.TrimSpace(r.MaterialID) == "" {
		return fmt.Errorf("%w: no material was named", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.CounterpartMaterialID) == "" {
		return fmt.Errorf("%w: no counterpart material was named", ErrInvalidRequest)
	}
	if r.MaterialID == r.CounterpartMaterialID {
		return fmt.Errorf("%w: a material cannot be paired with itself", ErrInvalidRequest)
	}
	return nil
}

// Bilingual is the answer to the one question a downstream bilingual behaviour
// asks before it does anything (§2.10): is bilingual reading enabled for this
// material, and if not, why not.
//
// It is a false-with-a-reason and not an empty result on purpose. A downstream
// step that renders nothing and says nothing is indistinguishable from a material
// that has no counterpart, and only one of those two is the reader's to fix.
type Bilingual struct {
	MaterialID string
	Enabled    bool
	// Pairing is the confirmed row, oriented from MaterialID: the material asked
	// about is PrimaryMaterialID and the far side is CounterpartMaterialID.
	Pairing Pairing
	// Reason is why bilingual reading is not enabled, and it is non-empty exactly
	// when Enabled is false.
	Reason string
}

// Pairings is one answer to the pairing read: the state of this material's
// bilingual reading, and the counterparts a reader could confirm.
type Pairings struct {
	Bilingual  Bilingual
	Candidates []Candidate
}

// materialFacts is what the pairing rules learned from one material's text: how
// long it is and what language it is in. Both come out of one read of one window,
// and the language is empty when the material names no document, when its text
// says nothing this build can tell apart, or when nothing needed to know.
type materialFacts struct {
	TextChars int64
	Language  string
}

// Pairings answers the pairing read (§2.10).
//
// The recommendation is computed here, per request, from the catalogue — and it
// is not computed at all for a material that is already paired: a reader who has
// confirmed a counterpart is not asking to be offered another one.
func (s *Service) Pairings(ctx context.Context, materialID string, caller authz.Caller) (Pairings, error) {
	material, err := s.repository.Material(ctx, strings.TrimSpace(materialID), caller)
	if err != nil {
		return Pairings{}, err
	}
	state, facts, err := s.bilingualOf(ctx, material, caller)
	if err != nil {
		return Pairings{}, err
	}
	answer := Pairings{Bilingual: state, Candidates: []Candidate{}}
	if state.Enabled || facts.Language == "" {
		return answer, nil
	}
	candidates, err := s.candidatesOf(ctx, material, facts, caller)
	if err != nil {
		return Pairings{}, err
	}
	answer.Candidates = candidates
	return answer, nil
}

// Bilingual answers the downstream question for one material, and it is the same
// answer the pairing read carries: one rule, two doors onto it.
func (s *Service) Bilingual(ctx context.Context, materialID string, caller authz.Caller) (Bilingual, error) {
	material, err := s.repository.Material(ctx, strings.TrimSpace(materialID), caller)
	if err != nil {
		return Bilingual{}, err
	}
	state, _, err := s.bilingualOf(ctx, material, caller)
	return state, err
}

// ConfirmPairing writes one confirmation.
//
// Nothing here is a new fact about the world: the confirmation is the reader's,
// and what this decides is whether it can be recorded. The pair that is already
// confirmed gets the row that is there; three things are refused and they are
// refused in the order a reader would want them — a material that is already
// paired with something else (the confirmation they may want instead is named),
// a material whose language cannot be read (there is nothing to compare), and two
// materials in the same language (they are not two versions of one book). The
// database makes the last two impossible as well — the CHECKs in migration 00012
// — and this is where the reader is told which one they hit.
//
// One material is in at most one pairing, and that is this service's rule rather
// than the table's: the downstream question is "which material is the other one",
// one answer, and a build where a material could sit in two pairings would have
// to pick one to answer with. Refusing the second confirmation says so out loud.
func (s *Service) ConfirmPairing(ctx context.Context, request PairingRequest, caller authz.Caller) (Pairing, error) {
	if err := request.Validate(); err != nil {
		return Pairing{}, err
	}
	primary, err := s.repository.Material(ctx, request.MaterialID, caller)
	if err != nil {
		return Pairing{}, err
	}
	counterpart, err := s.repository.Material(ctx, request.CounterpartMaterialID, caller)
	if err != nil {
		return Pairing{}, err
	}
	// Confirming what is already confirmed is answered with the row that is there
	// rather than refused: a reader who clicks twice asked for the state they
	// already have, and it is the same answer a cancel of nothing gets.
	confirmed, err := s.repository.PairingBetween(ctx, primary.MaterialID, counterpart.MaterialID, caller)
	if err == nil {
		return confirmed, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Pairing{}, err
	}
	for _, material := range []Material{primary, counterpart} {
		existing, err := s.repository.PairingOf(ctx, material.MaterialID, caller)
		if err == nil {
			other, _ := existing.Other(material.MaterialID)
			return Pairing{}, fmt.Errorf(
				"%w: material %s is already paired with %s, cancel that pairing first",
				ErrInvalidRequest, material.MaterialID, other)
		}
		if !errors.Is(err, ErrNotFound) {
			return Pairing{}, err
		}
	}
	primaryFacts, err := s.factsOf(ctx, primary, caller)
	if err != nil {
		return Pairing{}, err
	}
	counterpartFacts, err := s.factsOf(ctx, counterpart, caller)
	if err != nil {
		return Pairing{}, err
	}
	if primaryFacts.Language == "" || counterpartFacts.Language == "" {
		return Pairing{}, fmt.Errorf(
			"%w: the language of a material could not be read from its text, so the two cannot be confirmed as a pair",
			ErrInvalidRequest)
	}
	if primaryFacts.Language == counterpartFacts.Language {
		return Pairing{}, fmt.Errorf("%w: both materials are in %s, which is not a pair of two languages",
			ErrInvalidRequest, primaryFacts.Language)
	}
	pairingID, err := newID(pairingIDPrefix)
	if err != nil {
		return Pairing{}, fmt.Errorf("generate pairing id: %w", err)
	}
	pairing := Pairing{
		PairingID:             pairingID,
		PrimaryMaterialID:     primary.MaterialID,
		CounterpartMaterialID: counterpart.MaterialID,
		PrimaryLanguage:       primaryFacts.Language,
		CounterpartLanguage:   counterpartFacts.Language,
		CreatedAt:             time.Now().UTC(),
	}
	err = s.repository.CreatePairing(ctx, pairing, caller)
	if errors.Is(err, ErrDuplicate) {
		// Two confirmations of the same pair at the same instant: the row that won
		// is the confirmation, and handing it back is what makes a repeated click
		// an answer rather than a conflict.
		return s.repository.PairingBetween(ctx, primary.MaterialID, counterpart.MaterialID, caller)
	}
	if err != nil {
		return Pairing{}, err
	}
	return pairing, nil
}

// CancelPairing deletes one confirmation, and reports whether there was one to
// delete.
//
// A pairing that is not there is not an error, and that is a decision rather than
// a swallowed failure: the state the caller asked for — this material is not
// paired — is the state it is already in, which is the same answer the lease
// surface gives for a path nobody holds. A double-clicked cancel is a person
// using the interface, not a mistake to report.
//
// It is the whole of "cancel" because the row is the whole of the confirmation:
// removing it returns the material to not-enabled, and there is nothing else to
// undo — no derived text was ever made, and no job was ever queued (§2.10).
func (s *Service) CancelPairing(ctx context.Context, pairingID string, caller authz.Caller) (bool, error) {
	pairingID = strings.TrimSpace(pairingID)
	if pairingID == "" {
		return false, fmt.Errorf("%w: no pairing was named", ErrInvalidRequest)
	}
	err := s.repository.DeletePairing(ctx, pairingID, caller)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// bilingualOf answers the state for one material, and it hands back what it read
// from the material's text along with it because the recommendation is built from
// the same window: asking twice would be asking the knowledge base twice for one
// answer.
//
// A paired material is answered before its text is read at all. Nothing needs the
// language then — the row already records both — and a request whose answer is
// "enabled" should not fail because a document has become unreadable.
func (s *Service) bilingualOf(ctx context.Context, material Material, caller authz.Caller) (Bilingual, materialFacts, error) {
	pairing, err := s.repository.PairingOf(ctx, material.MaterialID, caller)
	if err == nil {
		return Bilingual{
			MaterialID: material.MaterialID,
			Enabled:    true,
			Pairing:    pairing.reorientedTo(material.MaterialID),
		}, materialFacts{}, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Bilingual{}, materialFacts{}, err
	}
	facts, err := s.factsOf(ctx, material, caller)
	if err != nil {
		return Bilingual{}, materialFacts{}, err
	}
	reason := pairingReasonNotPaired
	if facts.Language == "" {
		reason = pairingReasonNoLanguage
	}
	return Bilingual{MaterialID: material.MaterialID, Reason: reason}, facts, nil
}

// candidatesOf computes the counterparts this material could be paired with.
//
// It reads the catalogue and one window of each material in it, which is more
// reads than a build with a language column would need and fewer than it looks:
// the language is the only filter that can be applied before the text is read,
// and there is nowhere it is written down (§0.3). A material whose text cannot be
// read is skipped rather than failing the request — one unreadable book in a
// shelf is not the whole recommendation's problem — and one in the same language
// is skipped because two books in one language are not a translation pair.
func (s *Service) candidatesOf(ctx context.Context, material Material, facts materialFacts, caller authz.Caller) ([]Candidate, error) {
	all, err := s.repository.Materials(ctx, "", caller)
	if err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, len(all))
	for _, other := range all {
		if other.MaterialID == material.MaterialID {
			continue
		}
		otherFacts, err := s.factsOf(ctx, other, caller)
		if err != nil || otherFacts.Language == "" || otherFacts.Language == facts.Language {
			continue
		}
		candidates = append(candidates, Candidate{
			MaterialID: other.MaterialID,
			Filename:   other.Filename,
			Language:   otherFacts.Language,
			TextChars:  otherFacts.TextChars,
			Score: pairingScore(other.Filename, material.Filename,
				otherFacts.TextChars, facts.TextChars),
		})
	}
	// The order is by score and then by id, so that two candidates that score the
	// same come back in the same order every time: a recommendation whose order
	// changes between two identical reads is one nobody can talk about.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].MaterialID < candidates[j].MaterialID
	})
	if len(candidates) > pairingCandidateLimit {
		candidates = candidates[:pairingCandidateLimit]
	}
	return candidates, nil
}

// factsOf reads what the pairing rules need from one material's document.
//
// The window is read through the same port everything else in this package reads
// text through, so the offsets and the unit of "a character" stay V2's (§2.8),
// and a material with no document or no text answers with no language rather than
// with an error: a material row that names no document is a row the catalogue may
// still list.
func (s *Service) factsOf(ctx context.Context, material Material, caller authz.Caller) (materialFacts, error) {
	if strings.TrimSpace(material.ContentID) == "" {
		return materialFacts{}, nil
	}
	document, err := s.documents.Document(ctx, material.ContentID, caller)
	if err != nil {
		return materialFacts{}, err
	}
	facts := materialFacts{TextChars: document.TextChars}
	if document.TextChars <= 0 {
		return facts, nil
	}
	window, err := s.documents.Text(ctx, material.ContentID, 0, min(document.TextChars, languageWindow), caller)
	if err != nil {
		return materialFacts{}, err
	}
	facts.Language = languageOf(window)
	return facts, nil
}
