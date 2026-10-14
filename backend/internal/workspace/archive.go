package workspace

// The package: a ZIP of the objects, the manifest, and the manifest's digest.
//
// The order the members are written in is the format, and it is the order a
// reader wants them in. Objects first, because that is what a package is; then
// the manifest, which can only be written once every object's digest is known;
// then the manifest's own digest, which is written last because it is the seal
// on the thing in front of it. A package whose last member is missing is a
// package that was cut short, and finding that out should not require reading
// everything before it.
//
// The archive is *not* compressed. What a package holds is JSON, which gzip
// shrinks well, and compression is still not here: a package is written once and
// read once, both on the instance that produced or consumed it, so the bytes
// saved would be paid for on every read by a decompressor in the one path where
// a bug is expensive. Storing is also what makes the trailing member meaningful:
// a reader can hash a member as it arrives without keeping what it hashed.

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// fileMode is what a member is written as: readable, and not executable. A
// package holds data, and a member marked executable is a member that asks to be
// run by whatever unpacks it.
const fileMode = 0o644

// WriteArchive writes the objects, the manifest and the digest into w, and
// answers with the manifest it wrote.
//
// The objects are written in (kind, id) order rather than in the order the
// collector produced them, so that the same selection exported twice produces
// the same bytes. That is what lets a test compare two packages, and it is what
// makes "the second export changed nothing" a fact rather than a coincidence of
// map iteration.
func WriteArchive(w io.Writer, objects []Object) (Manifest, error) {
	ordered, err := orderObjects(objects)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{Entries: make([]Entry, 0, len(ordered))}
	for _, object := range ordered {
		manifest.Entries = append(manifest.Entries, Entry{
			Kind:   object.Kind,
			ID:     object.ID,
			SHA256: DigestOf(object.Data),
			Bytes:  int64(len(object.Data)),
		})
	}
	encoded, err := manifest.Encode()
	if err != nil {
		return Manifest{}, err
	}

	archive := zip.NewWriter(w)
	for _, object := range ordered {
		if err := writeMember(archive, MemberName(object.Ref), object.Data); err != nil {
			return Manifest{}, err
		}
	}
	if err := writeMember(archive, ManifestName, encoded); err != nil {
		return Manifest{}, err
	}
	// The digest's own line ends in a newline for the same reason every manifest
	// line does: a value cut short by a partial copy should read as a
	// disagreement rather than as a digest that happens to be short.
	if err := writeMember(archive, DigestName, []byte(DigestOf(encoded)+"\n")); err != nil {
		return Manifest{}, err
	}
	if err := archive.Close(); err != nil {
		return Manifest{}, fmt.Errorf("close the package: %w", err)
	}
	return manifest, nil
}

// MemberName is where an object lives inside a package.
//
// It is derived from the ref rather than stored, so that a package cannot hold
// two names for one object and so that a reader that has a manifest has the
// whole of the format. The ref is validated first, which is what keeps a member
// name from being a path anything could escape with.
func MemberName(ref Ref) string {
	return path.Join(string(ref.Kind), ref.ID)
}

// RefOfMember is the object a member name holds, for the members that hold one.
func RefOfMember(name string) (Ref, bool) {
	if name == ManifestName || name == DigestName {
		return Ref{}, false
	}
	kind, id, found := strings.Cut(name, "/")
	if !found {
		return Ref{}, false
	}
	ref := Ref{Kind: Kind(kind), ID: id}
	if err := ref.Validate(); err != nil {
		return Ref{}, false
	}
	return ref, true
}

// orderObjects sorts the objects and refuses anything a package may not hold.
//
// The refusal is the mechanical half of §12.5: a collector cannot put an account
// setting, a Memory, a credential or a call log into a package, because none of
// those is a kind the writer will write. It is not a filter that runs over the
// objects — it is the absence of anywhere for one to go.
func orderObjects(objects []Object) ([]Object, error) {
	ordered := make([]Object, len(objects))
	copy(ordered, objects)
	seen := make(map[Ref]struct{}, len(ordered))
	for _, object := range ordered {
		if err := object.Ref.Validate(); err != nil {
			return nil, err
		}
		if _, repeated := seen[object.Ref]; repeated {
			return nil, fmt.Errorf("workspace: %s was collected twice", object.Ref)
		}
		seen[object.Ref] = struct{}{}
	}
	sort.Slice(ordered, func(first, second int) bool {
		if ordered[first].Kind != ordered[second].Kind {
			return ordered[first].Kind < ordered[second].Kind
		}
		return ordered[first].ID < ordered[second].ID
	})
	return ordered, nil
}

func writeMember(archive *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(fileMode)
	member, err := archive.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("write %s into the package: %w", name, err)
	}
	if _, err := member.Write(data); err != nil {
		return fmt.Errorf("write %s into the package: %w", name, err)
	}
	return nil
}

// Archive is a package that has been read and checked against its own manifest.
type Archive struct {
	// Manifest is what the package says it holds.
	Manifest Manifest
	// Digest is the manifest's sha256 as the package recorded it and as it was
	// found to hold.
	Digest string
	// Objects are the members, keyed by ref. Only the objects the manifest names
	// are here: an extra member is reported in the report and not carried, so
	// that nothing downstream can write something the manifest did not promise.
	Objects map[Ref][]byte
	// Report is what verification found.
	Report Report
}

// Report is the difference between a package and its own manifest.
//
// It is a report rather than an error because the caller needs the parts and not
// the verdict: an import preview shows a user what is wrong with a package, and
// "this one file does not match" is more use than "the package is bad".
type Report struct {
	// Missing are objects the manifest names that the package does not hold.
	Missing []Ref
	// Mismatched are objects whose bytes hash to something other than what the
	// manifest says.
	Mismatched []Ref
	// Extra are members that are neither an object the manifest names nor one of
	// the package's own two.
	Extra []Ref
	// Unreadable are members that are in the package and the manifest and could
	// not be read out of the ZIP at all.
	Unreadable []Ref
}

// Clean reports whether the package and its manifest agree about everything.
func (r Report) Clean() bool {
	return len(r.Missing) == 0 && len(r.Mismatched) == 0 && len(r.Extra) == 0 && len(r.Unreadable) == 0
}

// Summary is the report as one sentence, for a failure code's company in a log
// line or an error.
func (r Report) Summary() string {
	return fmt.Sprintf("missing %d, mismatched %d, extra %d, unreadable %d",
		len(r.Missing), len(r.Mismatched), len(r.Extra), len(r.Unreadable))
}

// OpenArchive reads a package and checks every member against the manifest.
//
// It refuses bytes that are not a package at all, a package with no manifest, a
// package with no digest, and a package whose digest does not match the manifest
// it holds — because each of those is the package failing to be itself, which
// there is nothing to report *about*. A member that disagrees with the manifest
// is a different thing: the package is a package, and what is wrong is inside
// it, so it comes back in the report where a caller can name it.
//
// It takes an io.ReaderAt because a ZIP is read from its end: the central
// directory is the last thing in the file, and a reader that could only go
// forwards would have to hold the whole package to find it.
func OpenArchive(reader io.ReaderAt, size int64) (*Archive, error) {
	unpacked, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrBadPackage, "it is not a ZIP")
	}
	members, err := indexMembers(unpacked)
	if err != nil {
		return nil, err
	}

	manifestBytes, err := readNamed(members, ManifestName)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrBadPackage, "it holds no manifest")
	}
	digestBytes, err := readNamed(members, DigestName)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrBadPackage, "it holds no digest of its manifest")
	}
	if recorded := strings.TrimSpace(string(digestBytes)); recorded != DigestOf(manifestBytes) {
		return nil, fmt.Errorf("%w: %s", ErrDigestMismatch, "the manifest is not the one that was written")
	}
	manifest, err := DecodeManifest(manifestBytes)
	if err != nil {
		return nil, err
	}

	archive := &Archive{
		Manifest: manifest,
		Digest:   DigestOf(manifestBytes),
		Objects:  make(map[Ref][]byte, manifest.Len()),
	}
	named := make(map[Ref]Entry, manifest.Len())
	for _, entry := range manifest.Entries {
		named[entry.Ref()] = entry
		member, held := members[MemberName(entry.Ref())]
		if !held {
			archive.Report.Missing = append(archive.Report.Missing, entry.Ref())
			continue
		}
		data, err := readMember(member)
		if err != nil {
			archive.Report.Unreadable = append(archive.Report.Unreadable, entry.Ref())
			continue
		}
		if DigestOf(data) != entry.SHA256 || int64(len(data)) != entry.Bytes {
			archive.Report.Mismatched = append(archive.Report.Mismatched, entry.Ref())
			continue
		}
		archive.Objects[entry.Ref()] = data
	}
	// indexMembers has already refused every name that is not one of the two
	// members or a ref, so what is left here is a member the manifest does not
	// name — an object somebody added to a package, which is the one way a
	// package can hold more than it says it does.
	for name := range members {
		ref, isObject := RefOfMember(name)
		if !isObject {
			continue
		}
		if _, inManifest := named[ref]; !inManifest {
			archive.Report.Extra = append(archive.Report.Extra, ref)
		}
	}
	sortRefs(archive.Report.Missing)
	sortRefs(archive.Report.Mismatched)
	sortRefs(archive.Report.Extra)
	sortRefs(archive.Report.Unreadable)
	return archive, nil
}

// OpenArchiveBytes is OpenArchive over bytes already in hand.
func OpenArchiveBytes(data []byte) (*Archive, error) {
	return OpenArchive(bytes.NewReader(data), int64(len(data)))
}

// Objects returns the members in manifest order, which is the order the export
// wrote them and therefore the order an import should write them back.
func (a *Archive) ObjectsInOrder() []Object {
	objects := make([]Object, 0, len(a.Manifest.Entries))
	for _, entry := range a.Manifest.Entries {
		data, held := a.Objects[entry.Ref()]
		if !held {
			continue
		}
		objects = append(objects, Object{Ref: entry.Ref(), Data: data})
	}
	return objects
}

// indexMembers reads the ZIP's directory into a map, refusing a package with two
// members of one name.
//
// Two members of one name is what a ZIP made by concatenating two packages looks
// like, and every reader of such a file picks a different one of them. Refusing
// it here means no reader of this package ever has to.
func indexMembers(unpacked *zip.Reader) (map[string]*zip.File, error) {
	members := make(map[string]*zip.File, len(unpacked.File))
	for _, member := range unpacked.File {
		if !safeName(member.Name) {
			return nil, fmt.Errorf("%w: an entry name is not one this build writes", ErrBadPackage)
		}
		if _, repeated := members[member.Name]; repeated {
			return nil, fmt.Errorf("%w: %s appears twice", ErrBadPackage, member.Name)
		}
		members[member.Name] = member
	}
	return members, nil
}

// safeName reports whether a member name is one this build could have written:
// one slash at most, and both halves shaped like a kind and an id.
func safeName(name string) bool {
	if name == ManifestName || name == DigestName {
		return true
	}
	_, ok := RefOfMember(name)
	return ok
}

func readNamed(members map[string]*zip.File, name string) ([]byte, error) {
	member, held := members[name]
	if !held {
		return nil, fmt.Errorf("%s is not in the package", name)
	}
	return readMember(member)
}

func readMember(member *zip.File) ([]byte, error) {
	opened, err := member.Open()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", member.Name, err)
	}
	defer opened.Close()
	// The member's own uncompressed size is the only bound available before
	// reading it, and a ZIP can declare a size it does not deliver. Reading the
	// declared size plus one byte is what turns "this member claims to be a
	// terabyte" into a refusal rather than an allocation.
	if member.UncompressedSize64 > uint64(maxMemberBytes) {
		return nil, fmt.Errorf("%s is larger than a member may be", member.Name)
	}
	data, err := io.ReadAll(io.LimitReader(opened, maxMemberBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", member.Name, err)
	}
	if int64(len(data)) > maxMemberBytes {
		return nil, fmt.Errorf("%s is larger than a member may be", member.Name)
	}
	return data, nil
}

// countingWriter is the bound on the way out.
//
// A package is written to a file whose size nobody knows until it is written, so
// the limit has to be enforced while writing rather than checked afterwards: a
// check after the fact is a check that has already put the bytes on the disk.
type countingWriter struct {
	inner    io.Writer
	written  int64
	maxBytes int64
}

func (w *countingWriter) Write(data []byte) (int, error) {
	if w.written+int64(len(data)) > w.maxBytes {
		return 0, fmt.Errorf("%w: the limit is %d bytes", ErrTooLarge, w.maxBytes)
	}
	written, err := w.inner.Write(data)
	w.written += int64(written)
	return written, err
}

// sortRefs puts a report's lists in a stable order, so that two runs over the
// same package produce the same report and a test can compare them.
func sortRefs(refs []Ref) {
	sort.Slice(refs, func(first, second int) bool {
		if refs[first].Kind != refs[second].Kind {
			return refs[first].Kind < refs[second].Kind
		}
		return refs[first].ID < refs[second].ID
	})
}

// maxMemberBytes bounds one member. It is the size a single object may reach
// before the package's own limit is the thing that refuses it, and it exists so
// that a member claiming an enormous size is refused before it is read.
const maxMemberBytes int64 = 1 << 31
