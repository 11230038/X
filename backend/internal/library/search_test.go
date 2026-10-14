package library

// The listing: one half of the library at a time, four narrowings, one account,
// and a page.
//
// The double it runs against is recycle_double_test.go's, and the ordering and
// filters it stands in for are pinned against the real table in
// repository_recycle_db_test.go. What is here is the service's own reading of a
// query — which filters reach storage at all, and what a page means.

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestSearchListsOneHalfOfTheLibraryAtATime: a listing is live or binned and
// never both, because a client showing both would have to filter every row to
// know which actions it may offer beside it.
func TestSearchListsOneHalfOfTheLibraryAtATime(t *testing.T) {
	service, _, _ := newRecycler(t)
	kept := store(t, service, "线性代数.md", "kept")
	thrown := store(t, service, "线性代数习题.md", "thrown")
	if _, err := service.SoftDelete(context.Background(), thrown.ID, testActor()); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}

	live, err := service.Search(context.Background(), SearchQuery{Caller: testCaller()})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(live) != 1 || live[0].ID != kept.ID {
		t.Errorf("the live listing is %+v, want only the file that was kept", live)
	}
	binned, err := service.Search(context.Background(), SearchQuery{Caller: testCaller(), Deleted: true})
	if err != nil {
		t.Fatalf("Search(the bin) error = %v", err)
	}
	if len(binned) != 1 || binned[0].ID != thrown.ID {
		t.Fatalf("the recycle bin is %+v, want only the file that was thrown away", binned)
	}
	// The bin says when, which is the one thing a binned row has that a live one
	// does not — and it is the column the CHECK keeps beside the flag.
	if binned[0].DeletedAt == nil {
		t.Error("the binned file is listed without the moment it was thrown away")
	}
}

// TestSearchNarrowsByAllFourFilters covers what the listing is for. The filename
// match is case-insensitive and literal, which is why "代数" finds both files
// whose names contain it and "线性*" finds neither: a user typing a name into a
// box means the name, not a pattern.
func TestSearchNarrowsByAllFourFilters(t *testing.T) {
	service, _, _ := newRecycler(t)
	first := store(t, service, "Linear-Algebra.md", "a")
	// The second file is in another category, and its bytes are a PDF because the
	// upload reads them to decide what the file is: a name alone is a claim, and
	// "b" is not a document any more than it is a song.
	store(t, service, "notes.pdf", "%PDF-1.4 "+strings.Repeat("x", 16)+" %%EOF")

	tests := []struct {
		name  string
		query SearchQuery
		// want is the count, and it is the count rather than a list because two of
		// these cases are "both files", and which one comes first is the ordering
		// test's business rather than this one's.
		want int
	}{
		{"a name in another case", SearchQuery{Filename: "linear-algebra"}, 1},
		{"a name in a case nobody typed", SearchQuery{Filename: "LINEAR-ALGEBRA"}, 1},
		{"a name that is a wildcard", SearchQuery{Filename: "Linear*"}, 0},
		{"a name nothing is called", SearchQuery{Filename: "微积分"}, 0},
		{"a category", SearchQuery{Category: "md"}, 1},
		{"a category nothing was stored under", SearchQuery{Category: "mp3"}, 0},
		{"everything from the beginning of time", SearchQuery{Since: atSecond(0)}, 2},
		{"nothing before the beginning of time", SearchQuery{Until: atSecond(0)}, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.query.Caller = testCaller()
			found, err := service.Search(context.Background(), test.query)
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if len(found) != test.want {
				t.Fatalf("Search() found %d files, want %d", len(found), test.want)
			}
			if test.want == 1 && found[0].ID != first.ID {
				t.Errorf("Search() found %s, want %s", found[0].ID, first.ID)
			}
		})
	}
}

// TestSearchIsNarrowedToOneAccount is AUTH-06 arriving at the listing: two
// accounts that each upload a file with the same name must not see each other's,
// and the answer is a shorter list rather than a refusal.
func TestSearchIsNarrowedToOneAccount(t *testing.T) {
	service, repository, _ := newRecycler(t)
	mine := store(t, service, "习题.md", "mine")
	repository.stored["lib_theirs"] = FileRecord{
		ID: "lib_theirs", SHA256: strings.Repeat("c", 64), Filename: "习题.md",
		MIMEType: "text/markdown", LibraryPath: "md/lib_theirs.md",
		OwnerID: testOwnerID + 1, CreatedAt: atSecond(9), UpdatedAt: atSecond(9),
	}

	found, err := service.Search(context.Background(), SearchQuery{Caller: testCaller(), Filename: "习题"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(found) != 1 || found[0].ID != mine.ID {
		t.Errorf("the listing is %+v, want only this account's file", found)
	}
	// And the same query as the other account sees only theirs — the filter is
	// not "hide what is not mine", it is "list what is".
	found, err = service.Search(context.Background(), SearchQuery{Caller: otherCaller(), Filename: "习题"})
	if err != nil {
		t.Fatalf("Search() as the other account: %v", err)
	}
	if len(found) != 1 || found[0].ID != "lib_theirs" {
		t.Errorf("the other account's listing is %+v, want their own file", found)
	}
}

// TestTheListingPagesAndClamps: a limit above the maximum is a client asking for
// more than it should get rather than an error it could correct, so it is
// clamped; an offset past the end is an empty page and not a failure.
func TestTheListingPagesAndClamps(t *testing.T) {
	service, repository, _ := newRecycler(t)
	// The five are written straight into the double's table rather than uploaded,
	// because the ids have to be known for the assertion about which page starts
	// where — and the table is made here, since Create is what makes it.
	repository.stored = make(map[string]FileRecord, 5)
	for index := 0; index < 5; index++ {
		id := fmt.Sprintf("lib_%032d", index)
		repository.stored[id] = FileRecord{
			ID: id, SHA256: fmt.Sprintf("%064d", index), Filename: "notes.md",
			MIMEType: "text/markdown", LibraryPath: "md/" + id + ".md", OwnerID: testOwnerID,
			CreatedAt: atSecond(index), UpdatedAt: atSecond(index),
		}
	}

	page, err := service.Search(context.Background(), SearchQuery{Caller: testCaller(), Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("the page holds %d files, want 2", len(page))
	}
	// Newest first, so the second page-from-one starts at the fourth file.
	if page[0].ID != "lib_00000000000000000000000000000003" {
		t.Errorf("the page starts at %s, want the fourth file", page[0].ID)
	}

	beyond, err := service.Search(context.Background(), SearchQuery{Caller: testCaller(), Offset: 99})
	if err != nil {
		t.Fatalf("Search(past the end) error = %v", err)
	}
	if len(beyond) != 0 {
		t.Errorf("a page past the end holds %d files, want none", len(beyond))
	}
}
