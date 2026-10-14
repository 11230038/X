package reading

// The bilingual pairing's storage contract, checked against a real seekdb.
//
// Two claims only a database can settle are here. The first is the one §7.1
// names as an acceptance of its own — a recommendation leaves the table with zero
// rows — and it is a claim about a write path that deliberately does not write,
// which is exactly the kind of claim a double agrees with for free. The second is
// the shape of the row: read from either end by an OR predicate, refused by the
// CHECKs and the unique index when it is not a pair at all, and taken away by the
// CASCADE when a material goes.
//
// The knowledge base is doubled and the storage is not, and the split is the
// point: what language a text is in is read through the port, and what a pairing
// row is is decided by migration 00012.

import (
	"context"
	"strings"
	"testing"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

// newPairingShelf builds the service over the real repository and a doubled
// knowledge base, and puts one book in each language this build can tell apart
// on the shelf.
func newPairingShelf(t *testing.T) (*Service, *GORMRepository, *data.Resource, *fakeDocuments) {
	t.Helper()
	repository, resource := newDBRepository(t)
	documents := newFakeDocuments()
	service, err := NewService(repository, documents, Options{UnitMaxChars: 120})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	storeMaterialRow(t, resource, "rm_zh", "doc_zh", atSecond(0), nil)
	storeMaterialRow(t, resource, "rm_en", "doc_en", atSecond(1), nil)
	documents.withDocument(Document{
		ID: "doc_zh", FileID: "lib_zh", Filename: "sicp-zh.md",
		TextChars: int64(len([]rune(chineseText))),
	}, chineseText)
	documents.withDocument(Document{
		ID: "doc_en", FileID: "lib_en", Filename: "sicp-en.md",
		TextChars: int64(len([]rune(englishText))),
	}, englishText)
	return service, repository, resource, documents
}

// TestDBARecommendationLeavesTheTableEmpty is §7.1's 「推荐不落表」 stated as a
// row count: the candidates are computed, handed back, and written nowhere.
func TestDBARecommendationLeavesTheTableEmpty(t *testing.T) {
	service, _, resource, _ := newPairingShelf(t)

	answer, err := service.Pairings(context.Background(), "rm_zh", dbCaller)
	if err != nil {
		t.Fatalf("Pairings() error = %v", err)
	}
	if len(answer.Candidates) != 1 || answer.Candidates[0].MaterialID != "rm_en" {
		t.Fatalf("the recommendation offered %+v, want the one other-language material",
			answer.Candidates)
	}
	if got := countRows(t, resource, "reading_pairings"); got != 0 {
		t.Errorf("a recommendation stored %d rows, want none", got)
	}
}

// TestDBAConfirmationIsOneRowReadFromEitherEnd pins the row and the SQL that
// reads it: either material finds it, and the pair is found whichever way round
// it is asked for.
func TestDBAConfirmationIsOneRowReadFromEitherEnd(t *testing.T) {
	service, repository, resource, _ := newPairingShelf(t)
	ctx := context.Background()

	confirmed, err := service.ConfirmPairing(ctx, PairingRequest{
		MaterialID: "rm_zh", CounterpartMaterialID: "rm_en",
	}, dbCaller)
	if err != nil {
		t.Fatalf("ConfirmPairing() error = %v", err)
	}
	if got := countRows(t, resource, "reading_pairings"); got != 1 {
		t.Fatalf("a confirmation stored %d rows, want one", got)
	}
	// The languages in the row are what the two texts said, and they are stored
	// rather than derived: a document re-parsed into another revision later must
	// not rewrite what the reader agreed to.
	var stored struct {
		PrimaryLanguage     string `gorm:"column:primary_language"`
		CounterpartLanguage string `gorm:"column:counterpart_language"`
	}
	err = resource.GORM().Raw("SELECT primary_language, counterpart_language "+
		"FROM reading_pairings WHERE pairing_id = ?", confirmed.PairingID).Scan(&stored).Error
	if err != nil {
		t.Fatalf("read the row back: %v", err)
	}
	if stored.PrimaryLanguage != languageChinese || stored.CounterpartLanguage != languageEnglish {
		t.Errorf("the row records %q and %q, want the two languages of the two texts",
			stored.PrimaryLanguage, stored.CounterpartLanguage)
	}

	found, err := repository.PairingOf(ctx, "rm_en", dbCaller)
	if err != nil {
		t.Fatalf("PairingOf() from the far end error = %v", err)
	}
	if found.PairingID != confirmed.PairingID {
		t.Errorf("the far end reads pairing %s, want %s", found.PairingID, confirmed.PairingID)
	}
	// Asked for the other way round, which is the OR in the statement and not
	// something a double can be wrong about.
	reversed, err := repository.PairingBetween(ctx, "rm_en", "rm_zh", dbCaller)
	if err != nil {
		t.Fatalf("PairingBetween() reversed error = %v", err)
	}
	if reversed.PairingID != confirmed.PairingID {
		t.Errorf("the pair read backwards is %s, want %s", reversed.PairingID, confirmed.PairingID)
	}

	// Confirming again is answered with the row that is there, and the unique
	// index is what makes that true even when two requests arrive together.
	again, err := service.ConfirmPairing(ctx, PairingRequest{
		MaterialID: "rm_zh", CounterpartMaterialID: "rm_en",
	}, dbCaller)
	if err != nil {
		t.Fatalf("ConfirmPairing() again error = %v", err)
	}
	if again.PairingID != confirmed.PairingID {
		t.Errorf("the second confirmation answered with %s, want %s", again.PairingID, confirmed.PairingID)
	}
	if got := countRows(t, resource, "reading_pairings"); got != 1 {
		t.Errorf("%d rows after confirming twice, want one", got)
	}
}

// TestDBThePairingTableRefusesItsOwnImpossibleRows is the schema's half of the
// rules the service states in sentences: a pair of two languages that are the
// same, a material paired with itself, a blank id, and a material that is not
// there.
func TestDBThePairingTableRefusesItsOwnImpossibleRows(t *testing.T) {
	_, _, resource, _ := newPairingShelf(t)

	for _, scenario := range []struct {
		name  string
		query string
	}{
		{"the two languages are the same",
			"INSERT INTO reading_pairings (pairing_id, primary_material_id, " +
				"counterpart_material_id, primary_language, counterpart_language) " +
				"VALUES ('rp_1', 'rm_zh', 'rm_en', 'zh', 'zh')"},
		{"the material is paired with itself",
			"INSERT INTO reading_pairings (pairing_id, primary_material_id, " +
				"counterpart_material_id, primary_language, counterpart_language) " +
				"VALUES ('rp_1', 'rm_zh', 'rm_zh', 'zh', 'en')"},
		{"the pairing has no id",
			"INSERT INTO reading_pairings (pairing_id, primary_material_id, " +
				"counterpart_material_id, primary_language, counterpart_language) " +
				"VALUES ('', 'rm_zh', 'rm_en', 'zh', 'en')"},
		{"one side is a material that is not there",
			"INSERT INTO reading_pairings (pairing_id, primary_material_id, " +
				"counterpart_material_id, primary_language, counterpart_language) " +
				"VALUES ('rp_1', 'rm_missing', 'rm_en', 'zh', 'en')"},
	} {
		if err := testdb.Try(resource, scenario.query); err == nil {
			t.Errorf("%s was accepted, want the schema to refuse it", scenario.name)
		}
	}
	if got := countRows(t, resource, "reading_pairings"); got != 0 {
		t.Errorf("%d pairings are stored, want none", got)
	}
}

// TestDBThePairingIndexIsOnTheOrderedPair says where the uniqueness of "this
// pair" lives and where it does not.
//
// The index is on `(primary_material_id, counterpart_material_id)`, so it stops
// the same confirmation arriving twice and does not stop the same pair arriving
// the other way round. That is the service's rule and not the table's: the
// confirmation looks the pair up in both directions before it writes, and what
// this test pins is that a build which only had the table would have the gap.
func TestDBThePairingIndexIsOnTheOrderedPair(t *testing.T) {
	_, _, resource, _ := newPairingShelf(t)

	insert := "INSERT INTO reading_pairings (pairing_id, primary_material_id, " +
		"counterpart_material_id, primary_language, counterpart_language) VALUES (?, ?, ?, ?, ?)"
	testdb.Exec(t, resource, insert, "rp_1", "rm_zh", "rm_en", "zh", "en")
	if err := testdb.Try(resource, insert, "rp_2", "rm_zh", "rm_en", "zh", "en"); err == nil {
		t.Error("the same pair was taken twice, want the unique index to refuse it")
	}
	// The reversed row goes in, and that is the table's answer rather than a
	// mistake in this test: the index is ordered, so "the same pair the other way
	// round" is a different pair to it. The service closes the gap by looking the
	// pair up in both directions before it writes, and a test that fails here is
	// the index having become unordered — which is a change worth being told
	// about, not a break.
	if err := testdb.Try(resource, insert, "rp_2", "rm_en", "rm_zh", "en", "zh"); err != nil {
		t.Errorf("the reversed pair was refused: %v", err)
	}
	if got := countRows(t, resource, "reading_pairings"); got != 2 {
		t.Errorf("%d rows, want the two the table takes", got)
	}
}

// TestDBDeletingAMaterialTakesItsPairingWithIt is the CASCADE, and what the
// surviving material is told afterwards: not paired, with the reason a reader can
// act on.
func TestDBDeletingAMaterialTakesItsPairingWithIt(t *testing.T) {
	service, _, resource, _ := newPairingShelf(t)
	ctx := context.Background()

	if _, err := service.ConfirmPairing(ctx, PairingRequest{
		MaterialID: "rm_zh", CounterpartMaterialID: "rm_en",
	}, dbCaller); err != nil {
		t.Fatalf("ConfirmPairing() error = %v", err)
	}
	testdb.Exec(t, resource, "DELETE FROM reading_materials WHERE material_id = ?", "rm_en")

	if got := countRows(t, resource, "reading_pairings"); got != 0 {
		t.Errorf("%d pairings outlived the material they pointed at, want none", got)
	}
	state, err := service.Bilingual(ctx, "rm_zh", dbCaller)
	if err != nil {
		t.Fatalf("Bilingual() error = %v", err)
	}
	if state.Enabled || state.Reason != pairingReasonNotPaired {
		t.Errorf("the surviving material is %+v, want not enabled and told why", state)
	}
}

// TestDBCancellingRemovesTheRowAndTheStateWithIt closes the loop the requirement
// describes: the moment the row is gone the material is not enabled, and nothing
// else had to happen for that.
func TestDBCancellingRemovesTheRowAndTheStateWithIt(t *testing.T) {
	service, _, resource, _ := newPairingShelf(t)
	ctx := context.Background()

	confirmed, err := service.ConfirmPairing(ctx, PairingRequest{
		MaterialID: "rm_en", CounterpartMaterialID: "rm_zh",
	}, dbCaller)
	if err != nil {
		t.Fatalf("ConfirmPairing() error = %v", err)
	}
	cancelled, err := service.CancelPairing(ctx, confirmed.PairingID, dbCaller)
	if err != nil || !cancelled {
		t.Fatalf("CancelPairing() = %v, %v; want the row removed", cancelled, err)
	}
	if got := countRows(t, resource, "reading_pairings"); got != 0 {
		t.Errorf("%d rows left after cancelling, want none", got)
	}
	state, err := service.Bilingual(ctx, "rm_en", dbCaller)
	if err != nil {
		t.Fatalf("Bilingual() error = %v", err)
	}
	if state.Enabled || !strings.Contains(state.Reason, "no bilingual pairing") {
		t.Errorf("after cancelling the material is %+v, want not enabled", state)
	}
	// And there is nothing else to cancel: the row was the whole of it.
	again, err := service.CancelPairing(ctx, confirmed.PairingID, dbCaller)
	if err != nil || again {
		t.Errorf("cancelling twice = %v, %v; want the state it is already in", again, err)
	}
}
