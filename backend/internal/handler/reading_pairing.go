package handler

import (
	"net/http"
	"strings"
	"time"

	"backend/internal/httpx"
	"backend/internal/reading"
	"github.com/gin-gonic/gin"
)

// The bilingual pairing surface (§2.10): what a material could be paired with,
// what it is paired with, and the two writes that change that.
//
// The read carries both halves in one answer, because they are one question from
// the reader's side — "is this book bilingual, and if not what could it be" — and
// because the state is what every downstream behaviour asks before it does
// anything. That state is the shape an extension's availability has: enabled, or
// not enabled with a reason.

// bilingualView is whether bilingual reading is enabled for one material, and
// when it is not, why.
//
// Reason is non-empty exactly when Enabled is false, and that is the point of the
// shape: a downstream step that rendered nothing and said nothing would be
// indistinguishable from a material that has no counterpart, and only one of the
// two is the reader's to fix.
type bilingualView struct {
	MaterialID string       `json:"material_id"`
	Enabled    bool         `json:"enabled"`
	Reason     string       `json:"reason,omitempty"`
	Pairing    *pairingView `json:"pairing,omitempty"`
}

// pairingView is one confirmed pairing, oriented from the material that was
// asked about: PrimaryMaterialID is that one and CounterpartMaterialID is the far
// side, so a client never has to work out which end it is holding.
type pairingView struct {
	PairingID             string    `json:"pairing_id"`
	PrimaryMaterialID     string    `json:"primary_material_id"`
	CounterpartMaterialID string    `json:"counterpart_material_id"`
	PrimaryLanguage       string    `json:"primary_language"`
	CounterpartLanguage   string    `json:"counterpart_language"`
	CreatedAt             time.Time `json:"created_at"`
}

// candidateView is one material the catalogue offers as a counterpart.
//
// Score rides along so that the order can be disagreed with rather than only the
// list: a recommendation a reader cannot argue with is a verdict, and this one is
// a suggestion the reader confirms or ignores.
type candidateView struct {
	MaterialID string  `json:"material_id"`
	Filename   string  `json:"filename"`
	Language   string  `json:"language"`
	TextChars  int64   `json:"text_chars"`
	Score      float64 `json:"score"`
}

// pairingCancelledView is what cancelling answers: the state the caller asked
// for, which is that this pairing is not there.
type pairingCancelledView struct {
	PairingID string `json:"pairing_id"`
	Cancelled bool   `json:"cancelled"`
}

type confirmPairingRequest struct {
	// CounterpartMaterialID is the other language's material. The material being
	// paired is the route's, which is what keeps the two ends of a pair apart
	// rather than two ids in one body: the route names what the act is about and
	// the body says what to do with it.
	CounterpartMaterialID string `json:"counterpart_material_id"`
}

// Pairings returns the state of one material's bilingual reading and the
// counterparts it could be paired with (§2.10).
//
// The candidates are computed per request and stored nowhere, so this read writes
// nothing at all: that is what makes a row in the pairing table mean the reader
// confirmed it, and nothing weaker.
func (h *ReadingHandler) Pairings(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	answer, err := h.readings.Pairings(c.Request.Context(), c.Param("materialID"), account)
	if err != nil {
		h.fail(c, "list_pairings_failed", err)
		return
	}
	candidates := make([]candidateView, 0, len(answer.Candidates))
	for _, candidate := range answer.Candidates {
		candidates = append(candidates, candidateView{
			MaterialID: candidate.MaterialID, Filename: candidate.Filename,
			Language: candidate.Language, TextChars: candidate.TextChars,
			Score: candidate.Score,
		})
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{
		"bilingual":  toBilingualView(answer.Bilingual),
		"candidates": candidates,
	})
}

// ConfirmPairing records one reader's decision that two materials are the same
// book in two languages.
//
// What comes back is the row that was written, and nothing else is said about it:
// whether bilingual reading is now enabled is the read's answer, and a write that
// also asserted the state it produced would be a second place for that answer to
// live.
func (h *ReadingHandler) ConfirmPairing(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	materialID := c.Param("materialID")
	var request confirmPairingRequest
	if !readJSON(c, maxReadingRequestBytes, &request) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	pairing, err := h.readings.ConfirmPairing(c.Request.Context(), reading.PairingRequest{
		MaterialID:            materialID,
		CounterpartMaterialID: strings.TrimSpace(request.CounterpartMaterialID),
	}, account)
	if err != nil {
		h.fail(c, "confirm_pairing_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, gin.H{"pairing": toPairingView(pairing)})
}

// CancelPairing drops one confirmation.
//
// A pairing that is not there is 200 with `cancelled: false` rather than a 404,
// for the reason the lease surface gives: the state that was asked for is the
// state it is already in. The material goes back to "not enabled" the moment the
// row is gone, and nothing else has to happen for that — there is no derived text
// to remove (§2.10).
func (h *ReadingHandler) CancelPairing(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	pairingID := c.Param("pairingID")
	account, ok := caller(c)
	if !ok {
		return
	}
	cancelled, err := h.readings.CancelPairing(c.Request.Context(), pairingID, account)
	if err != nil {
		h.fail(c, "cancel_pairing_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, pairingCancelledView{
		PairingID: pairingID, Cancelled: cancelled,
	})
}

func toBilingualView(state reading.Bilingual) bilingualView {
	view := bilingualView{
		MaterialID: state.MaterialID,
		Enabled:    state.Enabled,
		Reason:     state.Reason,
	}
	if state.Enabled {
		confirmed := toPairingView(state.Pairing)
		view.Pairing = &confirmed
	}
	return view
}

func toPairingView(pairing reading.Pairing) pairingView {
	return pairingView{
		PairingID:             pairing.PairingID,
		PrimaryMaterialID:     pairing.PrimaryMaterialID,
		CounterpartMaterialID: pairing.CounterpartMaterialID,
		PrimaryLanguage:       pairing.PrimaryLanguage,
		CounterpartLanguage:   pairing.CounterpartLanguage,
		CreatedAt:             pairing.CreatedAt,
	}
}
