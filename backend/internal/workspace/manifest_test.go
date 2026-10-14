package workspace

// The manifest's format (§2.13).
//
// Every claim here is about bytes: what a manifest is, what one line of it
// holds, and what it refuses. A manifest is the only part of a package another
// implementation would have to read, so the format is pinned rather than
// described — a test that asserted "the manifest round-trips" without looking at
// a line would pass for a JSON array, for a map, and for any other shape this
// build might have picked.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func entryOf(kind Kind, id string, data []byte) Entry {
	return Entry{Kind: kind, ID: id, SHA256: DigestOf(data), Bytes: int64(len(data))}
}

func sampleManifest() Manifest {
	return Manifest{Entries: []Entry{
		entryOf(KindSession, "ses_1", []byte("one")),
		entryOf(KindMaterial, "mat_1", []byte("two")),
		entryOf(KindSession, "ses_2", []byte("three")),
	}}
}

func TestAManifestRoundTrips(t *testing.T) {
	source := sampleManifest()
	encoded, err := source.Encode()
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	decoded, err := DecodeManifest(encoded)
	if err != nil {
		t.Fatalf("DecodeManifest() error = %v", err)
	}
	if len(decoded.Entries) != len(source.Entries) {
		t.Fatalf("decoded %d entries, want %d", len(decoded.Entries), len(source.Entries))
	}
	for index, want := range source.Entries {
		if got := decoded.Entries[index]; got != want {
			t.Errorf("entry %d = %+v, want %+v", index, got, want)
		}
	}
	// The order is the order written, not sorted. The manifest is a record of
	// what a package holds in the order it holds it, and a reader that reordered
	// it would be answering a different question than the writer did.
	if decoded.Entries[0].ID != "ses_1" || decoded.Entries[2].ID != "ses_2" {
		t.Errorf("the order was not preserved: %+v", decoded.Entries)
	}
}

// TestOneEntryIsOneLine is the format itself.
func TestOneEntryIsOneLine(t *testing.T) {
	encoded, err := sampleManifest().Encode()
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if !bytes.HasSuffix(encoded, []byte("\n")) {
		t.Errorf("the manifest does not end in a newline, so a truncation reads as a shorter manifest")
	}
	lines := bytes.Split(bytes.TrimSuffix(encoded, []byte("\n")), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("the manifest is %d lines, want one per entry (3)", len(lines))
	}
	for index, line := range lines {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(line, &fields); err != nil {
			t.Fatalf("line %d is not a JSON object: %v", index, err)
		}
		// Exactly the four names §2.13 writes down. Asserting the set rather
		// than each field is what pins the format: a fifth field is a field
		// another implementation's reader would have to decide about.
		want := []string{"bytes", "id", "kind", "sha256"}
		if len(fields) != len(want) {
			t.Errorf("line %d holds %d fields, want %d: %s", index, len(fields), len(want), line)
		}
		for _, name := range want {
			if _, held := fields[name]; !held {
				t.Errorf("line %d has no %q: %s", index, name, line)
			}
		}
	}
}

func TestAManifestRefusesAnEntryItMayNotWrite(t *testing.T) {
	tests := []struct {
		name  string
		entry Entry
	}{
		{"a kind outside the set", Entry{Kind: "memory", ID: "mem_1", SHA256: DigestOf(nil)}},
		{"an id carrying a slash", Entry{Kind: KindSession, ID: "ses/1", SHA256: DigestOf(nil)}},
		{"an id that walks up", Entry{Kind: KindSession, ID: "..", SHA256: DigestOf(nil)}},
		{"a blank id", Entry{Kind: KindSession, ID: "", SHA256: DigestOf(nil)}},
		{"a digest that is not one", Entry{Kind: KindSession, ID: "ses_1", SHA256: "deadbeef"}},
		{"an uppercase digest", Entry{Kind: KindSession, ID: "ses_1", SHA256: strings.ToUpper(DigestOf(nil))}},
		{"a negative size", Entry{Kind: KindSession, ID: "ses_1", SHA256: DigestOf(nil), Bytes: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := (Manifest{Entries: []Entry{test.entry}}).Encode(); err == nil {
				t.Fatalf("Encode() error = nil, want a refusal for %+v", test.entry)
			}
		})
	}
}

func TestDecodingRefusesWhatItCannotRead(t *testing.T) {
	good, err := sampleManifest().Encode()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body string
	}{
		{"a line that is not JSON", "{not json}\n"},
		{"a line that is a JSON array", "[1, 2, 3]\n"},
		{"a line with a field this build does not know", `{"kind":"session","id":"ses_1","sha256":"` + DigestOf(nil) + `","bytes":0,"mode":"rw"}` + "\n"},
		{"a line naming an unknown kind", `{"kind":"apikey","id":"k","sha256":"` + DigestOf(nil) + `","bytes":0}` + "\n"},
		{"two lines for one object", fmt.Sprintf("%s%s", good, string(good))},
		{"a truncated last line", string(good[:len(good)-8])},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeManifest([]byte(test.body))
			if err == nil {
				t.Fatalf("DecodeManifest() error = nil, want a refusal")
			}
			if !errors.Is(err, ErrBadPackage) {
				t.Errorf("error = %v, want it to be ErrBadPackage", err)
			}
		})
	}
}

// TestAnEmptyManifestIsAnEmptyList: a package with nothing in it is a package, it
// is just one nobody should have asked for. The writer is not where that is
// refused — the export refuses an empty *selection* — so an empty manifest has
// to encode and decode rather than fail at a layer that cannot explain itself.
func TestAnEmptyManifestIsAnEmptyList(t *testing.T) {
	encoded, err := (Manifest{}).Encode()
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if len(encoded) != 0 {
		t.Fatalf("an empty manifest encoded to %q, want nothing at all", encoded)
	}
	decoded, err := DecodeManifest(encoded)
	if err != nil {
		t.Fatalf("DecodeManifest() error = %v", err)
	}
	if decoded.Len() != 0 {
		t.Errorf("decoded %d entries from nothing", decoded.Len())
	}
}

func TestTheManifestCountsWhatItHolds(t *testing.T) {
	manifest := sampleManifest()
	counts := manifest.Counts()
	if counts[string(KindSession)] != 2 || counts[string(KindMaterial)] != 1 {
		t.Errorf("counts = %v, want two sessions and one material", counts)
	}
	// A kind with nothing in the package is absent rather than zero. That keeps
	// "there are no questions here" and "nobody counted the questions" from
	// being the same reading of the same map.
	if _, mentioned := counts[string(KindQuestion)]; mentioned {
		t.Errorf("counts = %v, want no entry for a kind the package does not hold", counts)
	}
	if total := manifest.ObjectBytes(); total != int64(len("one")+len("two")+len("three")) {
		t.Errorf("ObjectBytes() = %d, want the sum of the objects", total)
	}
}

func TestTheDigestIsOfTheBytesAsWritten(t *testing.T) {
	manifest := sampleManifest()
	encoded, err := manifest.Encode()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := manifest.Digest()
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	if digest != DigestOf(encoded) {
		t.Fatalf("Digest() = %s, want the digest of the encoded manifest", digest)
	}
	if !isDigest(digest) {
		t.Errorf("Digest() = %q, want lowercase hex", digest)
	}
	// Re-encoding is what a reader does to check the digest, so it has to be
	// byte-for-byte stable. A manifest that encoded differently on a second call
	// would make every package it wrote look tampered with.
	again, err := manifest.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, again) {
		t.Errorf("encoding the same manifest twice produced different bytes")
	}
}

func TestEntryOfFindsTheOneItNames(t *testing.T) {
	manifest := sampleManifest()
	if entry, found := manifest.EntryOf(Ref{Kind: KindMaterial, ID: "mat_1"}); !found || entry.ID != "mat_1" {
		t.Errorf("EntryOf(material/mat_1) = %+v, %v", entry, found)
	}
	// The kind is part of the identity, and not only the id: two domains can
	// issue ids that look alike, and a lookup by id alone would answer with the
	// wrong one of them.
	if _, found := manifest.EntryOf(Ref{Kind: KindQuestion, ID: "mat_1"}); found {
		t.Errorf("EntryOf(question/mat_1) found something under the wrong kind")
	}
}
