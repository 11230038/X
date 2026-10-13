package kb

import (
	"math"
	"strings"
	"testing"
)

const (
	testK    = 60
	testTopK = 5
)

func ids(fused []Fused) []string {
	chunkIDs := make([]string, len(fused))
	for index, entry := range fused {
		chunkIDs[index] = entry.ChunkID
	}
	return chunkIDs
}

func mustFuse(t *testing.T, branches []Branch, k, topK int) []Fused {
	t.Helper()
	fused, err := Fuse(branches, k, topK)
	if err != nil {
		t.Fatalf("Fuse() error = %v", err)
	}
	return fused
}

func TestFuseScoresByRank(t *testing.T) {
	branches := []Branch{
		{Name: "vector", ChunkIDs: []string{"a", "b"}},
		{Name: "fulltext", ChunkIDs: []string{"b", "c"}},
	}

	fused := mustFuse(t, branches, testK, testTopK)

	// b is second in one path and first in the other, which beats a and c
	// being first in a single path: 1/62 + 1/61 > 1/61.
	if got := ids(fused); strings.Join(got, ",") != "b,a,c" {
		t.Fatalf("order = %v, want b,a,c", got)
	}

	want := map[string]float64{
		"a": 1.0 / (testK + 1),
		"b": 1.0/(testK+2) + 1.0/(testK+1),
		"c": 1.0 / (testK + 2),
	}
	for _, entry := range fused {
		if math.Abs(entry.Score-want[entry.ChunkID]) > 1e-12 {
			t.Errorf("score of %s = %v, want %v", entry.ChunkID, entry.Score, want[entry.ChunkID])
		}
	}
}

// A chunk both paths found is one span of text: it takes one place in the list
// and one citation number, not two.
func TestFuseMergesChunksBothPathsFound(t *testing.T) {
	branches := []Branch{
		{Name: "vector", ChunkIDs: []string{"a", "b", "c"}},
		{Name: "fulltext", ChunkIDs: []string{"c", "a"}},
	}

	fused := mustFuse(t, branches, testK, testTopK)

	if len(fused) != 3 {
		t.Fatalf("got %d results, want 3: %v", len(fused), ids(fused))
	}
	seen := map[string]int{}
	for _, entry := range fused {
		seen[entry.ChunkID]++
	}
	for chunkID, count := range seen {
		if count != 1 {
			t.Errorf("%s appears %d times", chunkID, count)
		}
	}

	for _, entry := range fused {
		switch entry.ChunkID {
		case "a":
			wantRanks(t, entry, rank{"vector", 1}, rank{"fulltext", 2})
		case "c":
			wantRanks(t, entry, rank{"vector", 3}, rank{"fulltext", 1})
		case "b":
			wantRanks(t, entry, rank{"vector", 2})
		}
	}
}

// rank is one expected placement, written out so a failure says which path and
// which place was wrong.
type rank struct {
	branch string
	at     int
}

func wantRanks(t *testing.T, entry Fused, want ...rank) {
	t.Helper()
	if len(entry.Ranks) != len(want) {
		t.Fatalf("%s was found by %d paths, want %d: %+v", entry.ChunkID, len(entry.Ranks), len(want), entry.Ranks)
	}
	for index, expected := range want {
		got := entry.Ranks[index]
		if got.Branch != expected.branch || got.Rank != expected.at {
			t.Errorf("%s, path %d: {%s, %d}, want {%s, %d}",
				entry.ChunkID, index, got.Branch, got.Rank, expected.branch, expected.at)
		}
	}
}

// The degradation the design leans on: with nothing from the vector path, the
// fusion is the full-text order and nothing fails.
func TestFuseWithOneBranchIsThatBranchsOrder(t *testing.T) {
	chunkIDs := []string{"a", "b", "c", "d", "e", "f"}
	branches := []Branch{{Name: "fulltext", ChunkIDs: chunkIDs}}

	fused := mustFuse(t, branches, testK, testTopK)

	if got := strings.Join(ids(fused), ","); got != "a,b,c,d,e" {
		t.Fatalf("order = %v, want that path's order cut at top k", got)
	}
	for index, entry := range fused {
		if entry.Ranks[0].Rank != index+1 {
			t.Errorf("rank of %s = %d, want %d", entry.ChunkID, entry.Ranks[0].Rank, index+1)
		}
	}
}

// An empty branch is not an error and does not weigh anything: a document whose
// vectors have not been computed yet simply does not appear in that path.
func TestFuseIgnoresBranchesWithNothingInThem(t *testing.T) {
	branches := []Branch{
		{Name: "vector", ChunkIDs: nil},
		{Name: "fulltext", ChunkIDs: []string{"a", "b"}},
	}

	fused := mustFuse(t, branches, testK, testTopK)

	if got := strings.Join(ids(fused), ","); got != "a,b" {
		t.Fatalf("order = %v, want a,b", got)
	}
}

func TestFuseWithNoBranchesReturnsNothing(t *testing.T) {
	fused := mustFuse(t, nil, testK, testTopK)
	if len(fused) != 0 {
		t.Fatalf("got %d results, want none", len(fused))
	}
}

func TestFuseCutsAtTopK(t *testing.T) {
	chunkIDs := make([]string, 0, 30)
	for index := 0; index < 30; index++ {
		chunkIDs = append(chunkIDs, string(rune('a'+index%26))+string(rune('0'+index/26)))
	}
	branches := []Branch{{Name: "vector", ChunkIDs: chunkIDs}}

	fused := mustFuse(t, branches, testK, 3)

	if len(fused) != 3 {
		t.Fatalf("got %d results, want 3", len(fused))
	}
	if got := strings.Join(ids(fused), ","); got != chunkIDs[0]+","+chunkIDs[1]+","+chunkIDs[2] {
		t.Fatalf("order = %v, want the first three of the branch", got)
	}
}

// Two identical queries returning the same rows must produce the same list: a
// citation that moves between two identical answers is a citation a reader
// cannot follow.
func TestFuseIsDeterministicWhenScoresTie(t *testing.T) {
	// c and d each lead one path and are second in the other, so their scores
	// are equal and the order has to come from somewhere other than the map.
	branches := []Branch{
		{Name: "vector", ChunkIDs: []string{"c", "d"}},
		{Name: "fulltext", ChunkIDs: []string{"d", "c"}},
	}

	first := mustFuse(t, branches, testK, testTopK)
	for attempt := 0; attempt < 20; attempt++ {
		again := mustFuse(t, branches, testK, testTopK)
		if strings.Join(ids(again), ",") != strings.Join(ids(first), ",") {
			t.Fatalf("run %d returned %v, first run returned %v", attempt, ids(again), ids(first))
		}
	}
	if first[0].Score != first[1].Score {
		t.Fatalf("the fixture is not a tie: %v and %v", first[0].Score, first[1].Score)
	}
}

func TestFuseCountsARepeatedChunkOnceAtItsBetterRank(t *testing.T) {
	branches := []Branch{{Name: "vector", ChunkIDs: []string{"b", "a", "a"}}}

	fused := mustFuse(t, branches, testK, testTopK)

	if len(fused) != 2 {
		t.Fatalf("got %d results, want 2: %v", len(fused), ids(fused))
	}
	for _, entry := range fused {
		if entry.ChunkID != "a" {
			continue
		}
		wantRanks(t, entry, rank{"vector", 2})
		if math.Abs(entry.Score-1.0/(testK+2)) > 1e-12 {
			t.Errorf("score of a = %v, want it counted once at rank 2", entry.Score)
		}
	}
}

// A zero constant is division by rank, which is a legitimate setting; a negative
// one would make the best ranks lose.
func TestFuseRefusesSettingsThatWouldRankBackwards(t *testing.T) {
	cases := []struct {
		name string
		k    int
		topK int
	}{
		{name: "negative k", k: -1, topK: 5},
		{name: "no top k", k: testK, topK: 0},
		{name: "negative top k", k: testK, topK: -1},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := Fuse(nil, testCase.k, testCase.topK); err == nil {
				t.Fatal("Fuse() error = nil, want an error")
			}
		})
	}
}

func TestFuseWithZeroKIsReciprocalRank(t *testing.T) {
	fused := mustFuse(t, []Branch{{Name: "vector", ChunkIDs: []string{"a", "b"}}}, 0, testTopK)

	if math.Abs(fused[0].Score-1.0) > 1e-12 {
		t.Errorf("score at rank 1 = %v, want 1", fused[0].Score)
	}
	if math.Abs(fused[1].Score-0.5) > 1e-12 {
		t.Errorf("score at rank 2 = %v, want 0.5", fused[1].Score)
	}
}
