package handler

// The bilingual pairing over HTTP (§2.10): the state a downstream step asks for,
// the recommendation that goes with it, and the two writes.
//
// The case worth having here is §7.1's acceptance in the form a client sees it —
// `enabled: false` with a reason on the wire, not an empty answer — because that
// is the failure the requirement names and the one a handler can quietly produce
// by omitting a field.

import (
	"fmt"
	"net/http"
	"testing"

	"backend/internal/reading"
)

func TestThePairingReadSaysNotEnabledWithAReason(t *testing.T) {
	stub := &readingStub{pairings: reading.Pairings{
		Bilingual: reading.Bilingual{
			MaterialID: "rm_zh",
			Reason:     "no bilingual pairing has been confirmed for this material",
		},
		Candidates: []reading.Candidate{{
			MaterialID: "rm_en", Filename: "sicp-en.md", Language: "en",
			TextChars: 4200, Score: 0.7,
		}},
	}}
	router := newReadingRouter(stub, nil)

	recorder := call(t, router, http.MethodGet, "/api/v1/reading/materials/rm_zh/pairings", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Bilingual  bilingualView   `json:"bilingual"`
			Candidates []candidateView `json:"candidates"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)

	// Not enabled, and not silent about it: a client that only saw an empty list
	// could not tell "you have not confirmed a counterpart" from "there is
	// nothing here", and only one of those is the reader's to fix.
	if body.Data.Bilingual.Enabled {
		t.Error("a material with no pairing came back enabled")
	}
	if body.Data.Bilingual.Reason == "" {
		t.Error("not enabled with no reason: the client cannot say why")
	}
	if body.Data.Bilingual.Pairing != nil {
		t.Errorf("a material with no pairing carried one: %+v", body.Data.Bilingual.Pairing)
	}
	if len(body.Data.Candidates) != 1 || body.Data.Candidates[0].MaterialID != "rm_en" {
		t.Errorf("the candidates are %+v, want the recommended material", body.Data.Candidates)
	}
	if body.Data.Candidates[0].Score != 0.7 {
		t.Errorf("the candidate scored %v on the wire, want the score it came with",
			body.Data.Candidates[0].Score)
	}
	if len(stub.paired) != 1 || stub.paired[0] != "rm_zh" {
		t.Errorf("the read asked for %v, want the material on the route", stub.paired)
	}
}

func TestThePairingReadNamesTheCounterpartWhenItIsEnabled(t *testing.T) {
	stub := &readingStub{pairings: reading.Pairings{
		Bilingual: reading.Bilingual{
			MaterialID: "rm_zh",
			Enabled:    true,
			Pairing: reading.Pairing{
				PairingID: "rp_1", PrimaryMaterialID: "rm_zh", CounterpartMaterialID: "rm_en",
				PrimaryLanguage: "zh", CounterpartLanguage: "en", CreatedAt: handlerReadingTime,
			},
		},
		Candidates: []reading.Candidate{},
	}}
	router := newReadingRouter(stub, nil)

	recorder := call(t, router, http.MethodGet, "/api/v1/reading/materials/rm_zh/pairings", "")
	var body struct {
		Data struct {
			Bilingual bilingualView `json:"bilingual"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)

	if !body.Data.Bilingual.Enabled || body.Data.Bilingual.Reason != "" {
		t.Errorf("the state is %+v, want enabled and with no reason", body.Data.Bilingual)
	}
	if body.Data.Bilingual.Pairing == nil {
		t.Fatal("enabled with no pairing on the wire")
	}
	if body.Data.Bilingual.Pairing.CounterpartMaterialID != "rm_en" ||
		body.Data.Bilingual.Pairing.CounterpartLanguage != "en" {
		t.Errorf("the pairing is %+v, want the counterpart and its language",
			body.Data.Bilingual.Pairing)
	}
}

func TestConfirmingPairingTakesTheRouteAndTheBody(t *testing.T) {
	stub := &readingStub{pairing: reading.Pairing{
		PairingID: "rp_1", PrimaryMaterialID: "rm_zh", CounterpartMaterialID: "rm_en",
		PrimaryLanguage: "zh", CounterpartLanguage: "en", CreatedAt: handlerReadingTime,
	}}
	router := newReadingRouter(stub, nil)

	recorder := call(t, router, http.MethodPost, "/api/v1/reading/materials/rm_zh/pairings",
		`{"counterpart_material_id":"  rm_en  "}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.confirms) != 1 {
		t.Fatalf("the service was asked to confirm %+v", stub.confirms)
	}
	// The material is the route's and the counterpart is the body's: the two ends
	// of a confirmation come from two places, so neither can be swapped for the
	// other by a client.
	asked := stub.confirms[0]
	if asked.MaterialID != "rm_zh" || asked.CounterpartMaterialID != "rm_en" {
		t.Errorf("the confirmation was %+v, want the route's material and the body's counterpart",
			asked)
	}
	var body struct {
		Data struct {
			Pairing pairingView `json:"pairing"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if body.Data.Pairing.PairingID != "rp_1" || body.Data.Pairing.CounterpartLanguage != "en" {
		t.Errorf("the confirmation answered %+v", body.Data.Pairing)
	}
}

func TestCancellingAPairingAnswersTheStateItIsAlreadyIn(t *testing.T) {
	stub := &readingStub{cancelled: false}
	router := newReadingRouter(stub, nil)

	recorder := call(t, router, http.MethodDelete, "/api/v1/reading/pairings/rp_1", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data pairingCancelledView `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if body.Data.Cancelled || body.Data.PairingID != "rp_1" {
		t.Errorf("cancelling a pairing that is not there answered %+v", body.Data)
	}
	if len(stub.cancels) != 1 || stub.cancels[0] != "rp_1" {
		t.Errorf("the service was asked to cancel %v", stub.cancels)
	}
}

// TestPairingRefusalsReachTheClient is the handler's half of the domain's
// sentinels: what the reader can act on is a 400 carrying the sentence the domain
// wrote, and what is not there is a 404.
func TestPairingRefusalsReachTheClient(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		failure    error
		wantStatus int
		wantInBody string
	}{
		{"a material that is not there", http.MethodGet,
			"/api/v1/reading/materials/rm_missing/pairings", "", reading.ErrNotFound,
			http.StatusNotFound, "no such material"},
		{"two materials in one language", http.MethodPost,
			"/api/v1/reading/materials/rm_zh/pairings", `{"counterpart_material_id":"rm_zh2"}`,
			fmt.Errorf("%w: both materials are in zh", reading.ErrInvalidRequest),
			http.StatusBadRequest, "both materials are in zh"},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			router := newReadingRouter(&readingStub{err: scenario.failure}, nil)
			recorder := call(t, router, scenario.method, scenario.path, scenario.body)
			if recorder.Code != scenario.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s",
					recorder.Code, scenario.wantStatus, recorder.Body.String())
			}
			if scenario.wantInBody != "" && !contains(recorder.Body.String(), scenario.wantInBody) {
				t.Errorf("the answer %q does not carry %q", recorder.Body.String(), scenario.wantInBody)
			}
		})
	}
}

// TestAPairingThatIsNotThereIsStillAnAnswer covers the one refusal that is
// answered rather than passed on. It is the domain that decides it — cancelling
// a pairing that is not there answers the state it is already in rather than an
// error (reading.Service.CancelPairing) — and what this pins is that the handler
// renders that decision as a 200 carrying `cancelled: false` rather than turning
// it into a failure of its own.
func TestAPairingThatIsNotThereIsStillAnAnswer(t *testing.T) {
	router := newReadingRouter(&readingStub{cancelled: false}, nil)

	recorder := call(t, router, http.MethodDelete, "/api/v1/reading/pairings/rp_1", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data pairingCancelledView `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if body.Data.Cancelled {
		t.Error("the answer says a pairing was removed when there was none")
	}
}
