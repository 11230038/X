package reading

// The bilingual pairing's rules, over the two doubles beside this file (§2.10).
//
// Three claims are pinned here and they are the three the design makes. A
// recommendation is a query: it computes an answer and stores none of it, so a
// row in `reading_pairings` means the reader confirmed it and nothing weaker. A
// confirmation is one row, and cancelling is that row gone. And a material that
// is not paired is answered with a reason rather than with nothing, because a
// downstream step that rendered nothing and said nothing is the failure this
// requirement names.
//
// What the language is read from is the fourth claim, and it is here rather than
// in a database test because it is a pure function over text: nothing in this
// build records what language a material is in, so the language is derived from
// the words, and the table below is the whole of what "derived" means.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The three texts the language reader is exercised on. Each is long enough to
// clear the floor below which a window is not evidence of anything.
var (
	chineseText = strings.Repeat("闭包捕获的是变量而不是值", 4)
	englishText = strings.Repeat("a closure captures the variable, not the value ", 4)
	// japaneseText is kana-heavy, which is what makes it Japanese rather than the
	// Chinese its Han characters would otherwise be read as.
	japaneseText = strings.Repeat("これはクロージャです", 6)
)

// TestLanguageIsReadFromTheTwoScriptsThisBuildKnows is the whole of the language
// rule.
//
// The empty answers matter as much as the two names: a window that says nothing —
// too short, all digits, or Japanese — is a window this build refuses to draw a
// conclusion from, and a material in an unknown language is not a candidate for
// anything rather than a candidate for the wrong thing.
func TestLanguageIsReadFromTheTwoScriptsThisBuildKnows(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"Chinese", chineseText, languageChinese},
		{"English", englishText, languageEnglish},
		// A Chinese book quoting English words is still a Chinese book: the script
		// that dominates is the answer, and Han dominates here.
		{"Chinese with English terms", chineseText + " closure capture variable", languageChinese},
		{"English with a Chinese title", englishText + "《算法笔记》", languageEnglish},
		// Neither script reaches the floor, so the text does not say.
		{"two characters", "闭包", ""},
		{"a few letters", "abc", ""},
		{"digits and punctuation", strings.Repeat("1234!? ", 20), ""},
		{"nothing at all", "", ""},
		// Japanese is written with Han characters and would be read as Chinese
		// without the kana refusal.
		{"Japanese", japaneseText, ""},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			if got := languageOf(scenario.text); got != scenario.want {
				t.Errorf("languageOf(%q) = %q, want %q", scenario.text, got, scenario.want)
			}
		})
	}
}

// sampleMaterialNamed is one material over one document, named as the file is
// named: the filename is half of what a recommendation is computed from, so a
// test about the order needs to decide it.
func sampleMaterialNamed(materialID, documentID, filename string) Material {
	return Material{
		MaterialID: materialID, ContentID: documentID, Filename: filename,
		SourceType: "library", CreatedAt: sampleTime,
	}
}

// withText puts one document and the material that reads it into the two
// doubles, with the length the rules read and the text they read taken from the
// same string.
func withText(repository *fakeRepository, documents *fakeDocuments,
	materialID, documentID, filename, text string) {
	document := Document{
		ID: documentID, FileID: "lib_" + documentID, Filename: filename,
		TextChars: int64(len([]rune(text))),
	}
	documents.withDocument(document, text)
	repository.withMaterial(sampleMaterialNamed(materialID, documentID, filename))
}

// pairedCatalogue is the smallest shelf that can be paired at all: one book in
// each language this build can tell apart.
func pairedCatalogue(t *testing.T) (*Service, *fakeRepository, *fakeDocuments) {
	t.Helper()
	service, repository, documents := newFakeService(t, 120)
	withText(repository, documents, "rm_zh", "doc_zh", "sicp-zh.md", chineseText)
	withText(repository, documents, "rm_en", "doc_en", "sicp-en.md", englishText)
	return service, repository, documents
}

// TestARecommendationStoresNothing is §2.10's first decision stated as a row
// count: the candidates are computed per request, so running a recommendation
// leaves the table exactly as empty as it was.
func TestARecommendationStoresNothing(t *testing.T) {
	service, repository, _ := pairedCatalogue(t)

	answer, err := service.Pairings(context.Background(), "rm_zh")
	if err != nil {
		t.Fatalf("Pairings() error = %v", err)
	}
	if len(answer.Candidates) != 1 {
		t.Fatalf("the recommendation offered %+v, want the one other-language material",
			answer.Candidates)
	}
	candidate := answer.Candidates[0]
	if candidate.MaterialID != "rm_en" || candidate.Language != languageEnglish {
		t.Errorf("the candidate is %+v, want the English material", candidate)
	}
	if candidate.TextChars != int64(len([]rune(englishText))) {
		t.Errorf("the candidate is %d characters long, want the document's own length",
			candidate.TextChars)
	}
	// The whole point: nothing was written. A recommendation that stored its
	// candidates would make "is this material paired" a question about a table full
	// of guesses.
	if len(repository.pairings) != 0 || len(repository.confirmed) != 0 {
		t.Errorf("a recommendation wrote %+v", repository.confirmed)
	}
}

// TestARecommendationLeavesOutItselfAndItsOwnLanguage is the one hard condition
// the recommendation has: two books in one language are not two versions of one
// book, and a material is not its own counterpart.
func TestARecommendationLeavesOutItselfAndItsOwnLanguage(t *testing.T) {
	service, repository, documents := pairedCatalogue(t)
	withText(repository, documents, "rm_zh2", "doc_zh2", "算法导论.md", chineseText)

	answer, err := service.Pairings(context.Background(), "rm_zh")
	if err != nil {
		t.Fatalf("Pairings() error = %v", err)
	}
	for _, candidate := range answer.Candidates {
		if candidate.MaterialID == "rm_zh" || candidate.MaterialID == "rm_zh2" {
			t.Errorf("the recommendation offered %s, which is not a counterpart",
				candidate.MaterialID)
		}
	}
	if len(answer.Candidates) != 1 {
		t.Errorf("the recommendation offered %+v, want only the English material",
			answer.Candidates)
	}
}

// TestTheRecommendationIsOrderedByWhatTheTwoFilesHaveInCommon is what makes the
// list a recommendation rather than a catalogue: the material whose filename
// names the same book comes first.
func TestTheRecommendationIsOrderedByWhatTheTwoFilesHaveInCommon(t *testing.T) {
	service, repository, documents := pairedCatalogue(t)
	withText(repository, documents, "rm_other", "doc_other", "unrelated-notes.md", englishText)

	answer, err := service.Pairings(context.Background(), "rm_zh")
	if err != nil {
		t.Fatalf("Pairings() error = %v", err)
	}
	if len(answer.Candidates) != 2 {
		t.Fatalf("the recommendation offered %+v, want both English materials",
			answer.Candidates)
	}
	if answer.Candidates[0].MaterialID != "rm_en" {
		t.Errorf("the recommendation leads with %s, want the material named for the same book",
			answer.Candidates[0].MaterialID)
	}
	if answer.Candidates[0].Score <= answer.Candidates[1].Score {
		t.Errorf("the scores are %v then %v, want the first one higher",
			answer.Candidates[0].Score, answer.Candidates[1].Score)
	}
}

// TestAnUnpairedMaterialIsAnsweredWithAReason is §7.1's acceptance: the answer
// to "is this bilingual" is "not enabled, and here is why" — never an empty
// result, and never a silence.
func TestAnUnpairedMaterialIsAnsweredWithAReason(t *testing.T) {
	service, repository, documents := pairedCatalogue(t)
	withText(repository, documents, "rm_empty", "doc_empty", "笔记.md", "")

	state, err := service.Bilingual(context.Background(), "rm_zh")
	if err != nil {
		t.Fatalf("Bilingual() error = %v", err)
	}
	if state.Enabled {
		t.Fatal("a material that was never paired reported bilingual reading enabled")
	}
	if state.Reason != pairingReasonNotPaired || state.Pairing.PairingID != "" {
		t.Errorf("the state is %+v, want not-enabled for the reason that nothing was confirmed",
			state)
	}
	// A material with no text to read says something different, because the
	// reader's next step is different: there is nothing here to compare.
	state, err = service.Bilingual(context.Background(), "rm_empty")
	if err != nil {
		t.Fatalf("Bilingual() error = %v", err)
	}
	if state.Enabled || state.Reason != pairingReasonNoLanguage {
		t.Errorf("the state is %+v, want not-enabled for the reason that the text says nothing",
			state)
	}
}

// TestConfirmingAndCancellingAreTheTwoStatesOfOneRow walks the acceptance in
// order: not enabled, confirmed, enabled, cancelled, not enabled again.
func TestConfirmingAndCancellingAreTheTwoStatesOfOneRow(t *testing.T) {
	service, repository, _ := pairedCatalogue(t)
	ctx := context.Background()

	confirmed, err := service.ConfirmPairing(ctx, PairingRequest{
		MaterialID: "rm_zh", CounterpartMaterialID: "rm_en",
	})
	if err != nil {
		t.Fatalf("ConfirmPairing() error = %v", err)
	}
	if !strings.HasPrefix(confirmed.PairingID, pairingIDPrefix) {
		t.Errorf("the pairing is called %q, want a row id", confirmed.PairingID)
	}
	if confirmed.PrimaryLanguage != languageChinese || confirmed.CounterpartLanguage != languageEnglish {
		t.Errorf("the languages were recorded as %q and %q",
			confirmed.PrimaryLanguage, confirmed.CounterpartLanguage)
	}
	if len(repository.confirmed) != 1 || len(repository.pairings) != 1 {
		t.Fatalf("a confirmation wrote %+v", repository.confirmed)
	}

	// The state is answered from the far end too, and oriented from whoever asked:
	// the English material is the primary side of a question about the English
	// material.
	state, err := service.Bilingual(ctx, "rm_en")
	if err != nil {
		t.Fatalf("Bilingual() error = %v", err)
	}
	if !state.Enabled || state.Reason != "" {
		t.Fatalf("a confirmed pairing is not enabled: %+v", state)
	}
	if state.Pairing.PrimaryMaterialID != "rm_en" || state.Pairing.CounterpartMaterialID != "rm_zh" {
		t.Errorf("the pairing reads back as %s -> %s, want it oriented from the material asked about",
			state.Pairing.PrimaryMaterialID, state.Pairing.CounterpartMaterialID)
	}
	if state.Pairing.CounterpartLanguage != languageChinese {
		t.Errorf("the far side is recorded as %q, want the language it is in",
			state.Pairing.CounterpartLanguage)
	}

	cancelled, err := service.CancelPairing(ctx, confirmed.PairingID)
	if err != nil {
		t.Fatalf("CancelPairing() error = %v", err)
	}
	if !cancelled || len(repository.pairings) != 0 {
		t.Fatalf("cancelling left %+v behind", repository.pairings)
	}
	state, err = service.Bilingual(ctx, "rm_zh")
	if err != nil {
		t.Fatalf("Bilingual() error = %v", err)
	}
	if state.Enabled || state.Reason != pairingReasonNotPaired {
		t.Errorf("after cancelling the state is %+v, want not enabled again", state)
	}
}

// TestCancellingNothingIsNotAFailure: the state that was asked for is the state
// it is already in, so a second click on cancel is answered rather than refused.
func TestCancellingNothingIsNotAFailure(t *testing.T) {
	service, _, _ := pairedCatalogue(t)

	cancelled, err := service.CancelPairing(context.Background(), "rp_没有这一行")
	if err != nil {
		t.Fatalf("CancelPairing() error = %v, want the state it is already in", err)
	}
	if cancelled {
		t.Error("cancelling a pairing that is not there reported that something was removed")
	}
}

// TestConfirmingTheSamePairTwiceWritesOneRow is the reader clicking twice: the
// second click is answered with the row the first one wrote, not refused.
func TestConfirmingTheSamePairTwiceWritesOneRow(t *testing.T) {
	service, repository, _ := pairedCatalogue(t)
	ctx := context.Background()
	request := PairingRequest{MaterialID: "rm_zh", CounterpartMaterialID: "rm_en"}

	first, err := service.ConfirmPairing(ctx, request)
	if err != nil {
		t.Fatalf("ConfirmPairing() error = %v", err)
	}
	second, err := service.ConfirmPairing(ctx, request)
	if err != nil {
		t.Fatalf("confirming it again = %v, want the row that is there", err)
	}
	if first.PairingID != second.PairingID {
		t.Errorf("the two confirmations are %s and %s, want one row", first.PairingID, second.PairingID)
	}
	if len(repository.confirmed) != 1 {
		t.Errorf("storage was handed %+v, want one confirmation", repository.confirmed)
	}
}

// TestAPairingIsRefusedWhenItIsNotOne collects the refusals, and each one is
// checked for the thing that matters: nothing was written.
func TestAPairingIsRefusedWhenItIsNotOne(t *testing.T) {
	cases := []struct {
		name    string
		request PairingRequest
	}{
		{"a material paired with itself",
			PairingRequest{MaterialID: "rm_zh", CounterpartMaterialID: "rm_zh"}},
		{"no counterpart named", PairingRequest{MaterialID: "rm_zh"}},
		{"no material named", PairingRequest{CounterpartMaterialID: "rm_en"}},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			service, repository, _ := pairedCatalogue(t)
			_, err := service.ConfirmPairing(context.Background(), scenario.request)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("ConfirmPairing(%+v) = %v, want ErrInvalidRequest", scenario.request, err)
			}
			if len(repository.confirmed) != 0 {
				t.Errorf("a refused confirmation wrote %+v", repository.confirmed)
			}
		})
	}
}

// TestTwoMaterialsInOneLanguageAreNotAPair is the refusal the database makes too
// (the CHECK in migration 00012), made here so the reader is told which rule they
// hit rather than shown a constraint violation.
func TestTwoMaterialsInOneLanguageAreNotAPair(t *testing.T) {
	service, repository, documents := pairedCatalogue(t)
	withText(repository, documents, "rm_zh2", "doc_zh2", "算法导论.md", chineseText)

	_, err := service.ConfirmPairing(context.Background(), PairingRequest{
		MaterialID: "rm_zh", CounterpartMaterialID: "rm_zh2",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("pairing two Chinese materials = %v, want ErrInvalidRequest", err)
	}
	if !strings.Contains(err.Error(), languageChinese) {
		t.Errorf("the refusal reads %q, want it to say which language both are in", err)
	}
	if len(repository.confirmed) != 0 {
		t.Errorf("a refused confirmation wrote %+v", repository.confirmed)
	}
}

// TestAMaterialInOnePairingIsNotInASecond is the service's own rule, which the
// table does not have: the downstream question is "which material is the other
// one", and a material in two pairings has two answers.
func TestAMaterialInOnePairingIsNotInASecond(t *testing.T) {
	service, repository, _ := pairedCatalogue(t)
	repository.withPairing(Pairing{
		PairingID: "rp_1", PrimaryMaterialID: "rm_zh", CounterpartMaterialID: "rm_fr",
		PrimaryLanguage: languageChinese, CounterpartLanguage: "fr", CreatedAt: sampleTime,
	})

	_, err := service.ConfirmPairing(context.Background(), PairingRequest{
		MaterialID: "rm_en", CounterpartMaterialID: "rm_zh",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("pairing a material that is already paired = %v, want ErrInvalidRequest", err)
	}
	// The sentence names the pairing that is in the way, because that is the one
	// the reader has to cancel first.
	if !strings.Contains(err.Error(), "rm_fr") {
		t.Errorf("the refusal reads %q, want it to name the pairing that is there", err)
	}
}

// TestAnAlreadyPairedMaterialIsNotOfferedAnother is the read's half of the same
// rule: a reader who has confirmed a counterpart is not asking for a second one.
func TestAnAlreadyPairedMaterialIsNotOfferedAnother(t *testing.T) {
	service, repository, _ := pairedCatalogue(t)
	repository.withPairing(Pairing{
		PairingID: "rp_1", PrimaryMaterialID: "rm_zh", CounterpartMaterialID: "rm_fr",
		PrimaryLanguage: languageChinese, CounterpartLanguage: "fr", CreatedAt: sampleTime,
	})

	answer, err := service.Pairings(context.Background(), "rm_zh")
	if err != nil {
		t.Fatalf("Pairings() error = %v", err)
	}
	if !answer.Bilingual.Enabled {
		t.Error("a material with a confirmed pairing reported not enabled")
	}
	if len(answer.Candidates) != 0 {
		t.Errorf("a paired material was offered %+v", answer.Candidates)
	}
}

// TestAPairedMaterialIsAnsweredWithoutReadingItsText pins a decision and not
// only a behaviour: the row already records both languages, so a request whose
// answer is "enabled" must not depend on a document still being readable.
func TestAPairedMaterialIsAnsweredWithoutReadingItsText(t *testing.T) {
	service, repository, documents := pairedCatalogue(t)
	repository.withPairing(Pairing{
		PairingID: "rp_1", PrimaryMaterialID: "rm_zh", CounterpartMaterialID: "rm_en",
		PrimaryLanguage: languageChinese, CounterpartLanguage: languageEnglish,
		CreatedAt: sampleTime,
	})

	documents.reads = nil
	if _, err := service.Bilingual(context.Background(), "rm_zh"); err != nil {
		t.Fatalf("Bilingual() error = %v", err)
	}
	if len(documents.reads) != 0 {
		t.Errorf("the text was read anyway: %v", documents.reads)
	}
}

// TestThePairingRulesReportWhatStorageSaid keeps the failures a caller has to be
// able to tell from a refusal.
func TestThePairingRulesReportWhatStorageSaid(t *testing.T) {
	service, repository, _ := pairedCatalogue(t)
	failure := errors.New("storage is down")
	repository.pairingErr = failure
	repository.createPairingErr = failure
	repository.deletePairingErr = failure
	ctx := context.Background()

	if _, err := service.Bilingual(ctx, "rm_zh"); !errors.Is(err, failure) {
		t.Errorf("Bilingual() error = %v, want the failure storage reported", err)
	}
	if _, err := service.Pairings(ctx, "rm_zh"); !errors.Is(err, failure) {
		t.Errorf("Pairings() error = %v, want the failure storage reported", err)
	}
	if _, err := service.ConfirmPairing(ctx, PairingRequest{
		MaterialID: "rm_zh", CounterpartMaterialID: "rm_en",
	}); !errors.Is(err, failure) {
		t.Errorf("ConfirmPairing() error = %v, want the failure storage reported", err)
	}
	if _, err := service.CancelPairing(ctx, "rp_1"); !errors.Is(err, failure) {
		t.Errorf("CancelPairing() error = %v, want the failure storage reported", err)
	}
}
