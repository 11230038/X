package workspace

// The manifest: one line per object, and the digest of the lines (§2.13).
//
// The format is JSON Lines and not one JSON array, and that is a decision about
// what a manifest is *for*. A package may hold tens of thousands of objects and
// the manifest is the first thing anything reads out of one — an array would
// have to be parsed whole before its first entry could be looked at, where a
// line at a time can be checked as it is read and a manifest that turns out to
// be truncated is detected at the line it stops rather than after a parser has
// given up on the whole thing.
//
// The digest is of the manifest's bytes, exactly as written, and it is stored in
// the package rather than in the row: what it has to survive is a copy of the
// file, and the row is on the other side of that copy.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// The two members every package ends with, in the order they are written.
const (
	// ManifestName is the member holding the entries.
	ManifestName = "manifest.json"
	// DigestName is the member holding the manifest's own sha256.
	DigestName = "manifest.sha256"
)

// Manifest is what a package holds: one entry per object, in the order the
// objects were written.
type Manifest struct {
	Entries []Entry
}

// Len is how many objects the manifest names.
func (m Manifest) Len() int { return len(m.Entries) }

// ObjectBytes is the total size of the objects the manifest names, which is not
// the size of the package: the package also holds the manifest and a ZIP's own
// bookkeeping.
func (m Manifest) ObjectBytes() int64 {
	var total int64
	for _, entry := range m.Entries {
		total += entry.Bytes
	}
	return total
}

// Counts is how many of each kind the manifest names, keyed by kind. A kind with
// nothing in the package is absent rather than zero, so that "no questions" and
// "questions were not counted" are not the same reading.
func (m Manifest) Counts() map[string]int {
	counts := make(map[string]int, len(Kinds))
	for _, entry := range m.Entries {
		counts[string(entry.Kind)]++
	}
	return counts
}

// EntryOf finds an entry by ref.
func (m Manifest) EntryOf(ref Ref) (Entry, bool) {
	for _, entry := range m.Entries {
		if entry.Kind == ref.Kind && entry.ID == ref.ID {
			return entry, true
		}
	}
	return Entry{}, false
}

// Encode writes the entries as JSON Lines, one object per line, each line
// terminated.
//
// Every trailing newline is deliberate: a manifest that ends without one is a
// manifest whose last line may have been cut short by a copy that stopped
// mid-write, and requiring the terminator turns that into a parse failure rather
// than into a shorter list that reads as complete.
func (m Manifest) Encode() ([]byte, error) {
	var buffer bytes.Buffer
	for index, entry := range m.Entries {
		if err := entry.validate(); err != nil {
			return nil, fmt.Errorf("manifest entry %d: %w", index, err)
		}
		line, err := json.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("encode manifest entry %d", index)
		}
		buffer.Write(line)
		buffer.WriteByte('\n')
	}
	return buffer.Bytes(), nil
}

// Digest is the sha256 of the encoded manifest, which is what goes in the
// package's last member.
func (m Manifest) Digest() (string, error) {
	encoded, err := m.Encode()
	if err != nil {
		return "", err
	}
	return DigestOf(encoded), nil
}

// DigestOf is the sha256 of a byte slice as lowercase hex.
func DigestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// DecodeManifest reads JSON Lines back.
//
// It refuses a body it does not understand rather than returning what it could
// parse, for the reason the audit's reader does: a manifest that quietly dropped
// a line would answer "this package does not hold that" for a package that does,
// and the whole point of a manifest is that the answer to that question is the
// one written down.
func DecodeManifest(data []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	// The entry's field set is the format, so a line carrying a field this build
	// does not know is a line written by something that means something else by
	// it — a refusal rather than a silent drop.
	decoder.DisallowUnknownFields()

	manifest := Manifest{}
	seen := make(map[Ref]struct{})
	for {
		var entry Entry
		err := decoder.Decode(&entry)
		if err == io.EOF {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("%w: %s", ErrBadPackage, describeLineError(err))
		}
		if err := entry.validate(); err != nil {
			return Manifest{}, fmt.Errorf("%w: %s", ErrBadPackage, err.Error())
		}
		ref := entry.Ref()
		if _, repeated := seen[ref]; repeated {
			// Two lines for one object is the shape a package takes when it was
			// concatenated rather than written, and a reader that took the first
			// or the last would be choosing which of two stated contents to
			// believe.
			return Manifest{}, fmt.Errorf("%w: %s appears twice", ErrBadPackage, ref)
		}
		seen[ref] = struct{}{}
		manifest.Entries = append(manifest.Entries, entry)
	}
	return manifest, nil
}

// validate refuses an entry this build may not write or read.
func (e Entry) validate() error {
	if err := (Ref{Kind: e.Kind, ID: e.ID}).Validate(); err != nil {
		return err
	}
	if !isDigest(e.SHA256) {
		return fmt.Errorf("%w: the digest of %s is not a sha256", ErrBadPackage, e.Ref())
	}
	if e.Bytes < 0 {
		return fmt.Errorf("%w: %s has a negative size", ErrBadPackage, e.Ref())
	}
	return nil
}

// describeLineError keeps the decoder's complaint but not its byte offset, which
// counts the whole body and would name a character position a caller cannot
// point at in a file it never held.
func describeLineError(err error) string {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return "a line is not a JSON object"
	}
	return "a line is not a manifest entry"
}
