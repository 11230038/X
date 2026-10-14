package workspace

// The package itself: what the writer writes, and what the reader catches.
//
// The four cases §7.1 asks for are here and they are four different claims:
// a package that round-trips, an object whose bytes changed by one, an object
// that is not there, and bytes that are not a package at all. A test that only
// round-tripped would pass for a writer and a reader that agreed about nothing —
// the two would simply be symmetric, and a package from anywhere else would be
// unreadable for reasons nobody had written down.

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func sampleObjects() []Object {
	return []Object{
		{Ref: Ref{Kind: KindSession, ID: "ses_1"}, Data: []byte(`{"session":{"title":"one"}}`)},
		{Ref: Ref{Kind: KindMaterial, ID: "mat_1"}, Data: []byte(`{"material":{"filename":"a.pdf"}}`)},
		{Ref: Ref{Kind: KindSession, ID: "ses_2"}, Data: []byte(`{"session":{"title":"two"}}`)},
	}
}

func writeSample(t *testing.T, objects []Object) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if _, err := WriteArchive(&buffer, objects); err != nil {
		t.Fatalf("WriteArchive() error = %v", err)
	}
	return buffer.Bytes()
}

func TestAPackageRoundTripsThroughItsManifest(t *testing.T) {
	objects := sampleObjects()
	pkg := writeSample(t, objects)

	archive, err := OpenArchiveBytes(pkg)
	if err != nil {
		t.Fatalf("OpenArchiveBytes() error = %v", err)
	}
	if !archive.Report.Clean() {
		t.Fatalf("the package does not match its own manifest: %s", archive.Report.Summary())
	}
	if archive.Manifest.Len() != len(objects) {
		t.Fatalf("the manifest names %d objects, want %d", archive.Manifest.Len(), len(objects))
	}
	// The order is the writer's (kind, id), and it is what an import writes back
	// in. Asserting it here is asserting that the same selection exported twice
	// produces the same package, which is what the digest is worth anything for.
	order := []Ref{}
	for _, object := range archive.ObjectsInOrder() {
		order = append(order, object.Ref)
	}
	want := []Ref{
		{Kind: KindMaterial, ID: "mat_1"},
		{Kind: KindSession, ID: "ses_1"},
		{Kind: KindSession, ID: "ses_2"},
	}
	if len(order) != len(want) {
		t.Fatalf("the package holds %d objects, want %d", len(order), len(want))
	}
	for index := range want {
		if order[index] != want[index] {
			t.Errorf("object %d is %s, want %s", index, order[index], want[index])
		}
	}
	for _, object := range objects {
		held, found := archive.Objects[object.Ref]
		if !found {
			t.Fatalf("%s did not come back out", object.Ref)
		}
		if !bytes.Equal(held, object.Data) {
			t.Errorf("%s came back as %q, want %q", object.Ref, held, object.Data)
		}
	}
}

func TestTheSameObjectsWriteTheSamePackage(t *testing.T) {
	// The objects are handed in a different order the second time. The writer
	// sorts, so the two packages are one package — and that is the property that
	// makes "export twice and compare" a test somebody can write.
	first := writeSample(t, sampleObjects())
	shuffled := sampleObjects()
	shuffled[0], shuffled[2] = shuffled[2], shuffled[0]
	second := writeSample(t, shuffled)
	if !bytes.Equal(first, second) {
		t.Errorf("the same objects written in a different order produced different packages")
	}
}

// TestAOneByteChangeIsCaught: an object that was edited after the manifest was
// written. It is the case a digest exists for, and the change is one byte — a
// size that survives every check except a hash.
func TestAOneByteChangeIsCaught(t *testing.T) {
	objects := sampleObjects()
	pkg := writeSample(t, objects)
	edited := rewriteMember(t, pkg, MemberName(Ref{Kind: KindMaterial, ID: "mat_1"}),
		func(data []byte) []byte {
			changed := append([]byte(nil), data...)
			changed[len(changed)-4] = 'z'
			return changed
		})

	archive, err := OpenArchiveBytes(edited)
	if err != nil {
		t.Fatalf("OpenArchiveBytes() error = %v, want a readable package with a bad object", err)
	}
	if archive.Report.Clean() {
		t.Fatalf("the change was not noticed: %s", archive.Report.Summary())
	}
	if len(archive.Report.Mismatched) != 1 {
		t.Fatalf("mismatched = %v, want exactly the edited object", archive.Report.Mismatched)
	}
	if got := archive.Report.Mismatched[0]; got != (Ref{Kind: KindMaterial, ID: "mat_1"}) {
		t.Errorf("mismatched = %s, want material/mat_1", got)
	}
	// The edited object is not carried. What a package holds is what its
	// manifest promised, and a reader that handed the edited bytes on would be
	// writing them into somebody's account on the strength of a name.
	if _, held := archive.Objects[Ref{Kind: KindMaterial, ID: "mat_1"}]; held {
		t.Errorf("the object that does not match its digest was still carried out of the archive")
	}
}

// TestAMissingObjectIsCaught: a manifest that names something the package does
// not hold. It is what a truncated copy looks like from the inside.
func TestAMissingObjectIsCaught(t *testing.T) {
	pkg := writeSample(t, sampleObjects())
	dropped := dropMember(t, pkg, MemberName(Ref{Kind: KindSession, ID: "ses_2"}))

	archive, err := OpenArchiveBytes(dropped)
	if err != nil {
		t.Fatalf("OpenArchiveBytes() error = %v", err)
	}
	if archive.Report.Clean() {
		t.Fatalf("the missing object was not noticed: %s", archive.Report.Summary())
	}
	if len(archive.Report.Missing) != 1 || archive.Report.Missing[0].ID != "ses_2" {
		t.Fatalf("missing = %v, want session/ses_2", archive.Report.Missing)
	}
	if archive.Manifest.Len() != 3 {
		t.Errorf("the manifest lost the object too: %d entries", archive.Manifest.Len())
	}
}

// TestAnObjectTheManifestDoesNotNameIsCaught: the other direction. It is what a
// package somebody added to looks like, and it matters because the manifest is
// the promise — an object nobody promised is an object an import would write
// without having shown it to anybody.
func TestAnObjectTheManifestDoesNotNameIsCaught(t *testing.T) {
	pkg := writeSample(t, sampleObjects())
	added := addMember(t, pkg, MemberName(Ref{Kind: KindQuestion, ID: "q_9"}), []byte(`{"entry":{}}`))

	archive, err := OpenArchiveBytes(added)
	if err != nil {
		t.Fatalf("OpenArchiveBytes() error = %v", err)
	}
	if archive.Report.Clean() {
		t.Fatalf("the added object was not noticed: %s", archive.Report.Summary())
	}
	if len(archive.Report.Extra) != 1 || archive.Report.Extra[0].ID != "q_9" {
		t.Fatalf("extra = %v, want question/q_9", archive.Report.Extra)
	}
	if _, carried := archive.Objects[Ref{Kind: KindQuestion, ID: "q_9"}]; carried {
		t.Errorf("an object the manifest does not name was carried out of the archive")
	}
}

func TestBytesThatAreNotAPackageAreRefused(t *testing.T) {
	tests := []struct {
		name   string
		body   []byte
		banned error
		reason string
	}{
		{"something else entirely", []byte("this is not a zip"), ErrBadPackage, "not a ZIP"},
		{"a zip with no manifest", zipOf(t, map[string][]byte{"session/ses_1": []byte("{}")}), ErrBadPackage, "no manifest"},
		{"a zip with a manifest and no digest", zipOf(t, map[string][]byte{
			ManifestName: []byte(`{"kind":"session","id":"ses_1","sha256":"` + DigestOf(nil) + `","bytes":0}` + "\n"),
		}), ErrBadPackage, "no digest"},
		{"a manifest that is not the one written", zipOf(t, map[string][]byte{
			ManifestName: []byte(`{"kind":"session","id":"ses_1","sha256":"` + DigestOf(nil) + `","bytes":0}` + "\n"),
			DigestName:   []byte(DigestOf([]byte("some other manifest")) + "\n"),
		}), ErrDigestMismatch, "the manifest is not the one that was written"},
		{"a member whose name is not one this build writes", zipOf(t, map[string][]byte{
			ManifestName:       []byte{},
			DigestName:         []byte(DigestOf([]byte{}) + "\n"),
			"../../etc/passwd": []byte("x"),
		}), ErrBadPackage, "entry name"},
		{"a member outside the kinds", zipOf(t, map[string][]byte{
			ManifestName: []byte{},
			DigestName:   []byte(DigestOf([]byte{}) + "\n"),
			"apikey/k_1": []byte("x"),
		}), ErrBadPackage, "entry name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := OpenArchiveBytes(test.body)
			if err == nil {
				t.Fatalf("OpenArchiveBytes() error = nil, want a refusal")
			}
			if !errors.Is(err, test.banned) {
				t.Errorf("error = %v, want it to be %v", err, test.banned)
			}
			// The reason names the part that could not be read. "It is not a
			// package" alone would leave a user with a file and no next step,
			// where "it holds no manifest" is something to say about it.
			if !strings.Contains(err.Error(), test.reason) {
				t.Errorf("error = %v, want it to mention %q", err, test.reason)
			}
		})
	}
}

// TestTheWriterRefusesWhatAPackageMayNotHold is the mechanical half of §12.5
// and AC-13.
//
// Account settings, Memory, credentials and llm_calls are not absent from a
// package because something filtered them out at the end. There is no kind for
// them, so a collector that tried to put one in is refused — and that is what
// this asserts: not that the writer skips it, but that the writer will not
// produce the package at all.
func TestTheWriterRefusesWhatAPackageMayNotHold(t *testing.T) {
	for _, kind := range []Kind{"account", "memory", "credential", "llm_call", "user", "audit_event", "usage"} {
		t.Run(string(kind), func(t *testing.T) {
			var buffer bytes.Buffer
			_, err := WriteArchive(&buffer, []Object{
				{Ref: Ref{Kind: kind, ID: "x_1"}, Data: []byte(`{"secret":"…"}`)},
			})
			if err == nil {
				t.Fatalf("WriteArchive() packed a %s, want a refusal", kind)
			}
			if !errors.Is(err, ErrUnknownKind) {
				t.Errorf("error = %v, want ErrUnknownKind", err)
			}
			if buffer.Len() != 0 {
				t.Errorf("a refused package wrote %d bytes before being refused", buffer.Len())
			}
		})
	}
}

func TestTheWriterRefusesAnIdThatCouldEscape(t *testing.T) {
	tests := []struct{ name, id string }{
		{"a slash", "ses/../../elsewhere"},
		{"a walk up", ".."},
		{"a space", "ses 1"},
		{"a newline", "ses\n1"},
		{"nothing", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buffer bytes.Buffer
			_, err := WriteArchive(&buffer, []Object{
				{Ref: Ref{Kind: KindSession, ID: test.id}, Data: []byte("{}")},
			})
			if err == nil {
				t.Fatalf("WriteArchive() accepted the id %q", test.id)
			}
			if !errors.Is(err, ErrInvalidRef) {
				t.Errorf("error = %v, want ErrInvalidRef", err)
			}
		})
	}
}

func TestTheWriterRefusesOneObjectTwice(t *testing.T) {
	var buffer bytes.Buffer
	_, err := WriteArchive(&buffer, []Object{
		{Ref: Ref{Kind: KindSession, ID: "ses_1"}, Data: []byte("{}")},
		{Ref: Ref{Kind: KindSession, ID: "ses_1"}, Data: []byte(`{"other":true}`)},
	})
	if err == nil {
		t.Fatalf("WriteArchive() packed one object twice")
	}
}

// TestTheManifestIsTheLastTwoMembers is the order the format promises. A reader
// that has to keep going after the manifest is a reader that cannot check a
// package while it streams.
func TestTheManifestIsTheLastTwoMembers(t *testing.T) {
	unpacked, err := zip.NewReader(bytes.NewReader(writeSample(t, sampleObjects())), int64(len(writeSample(t, sampleObjects()))))
	if err != nil {
		t.Fatalf("the package is not a ZIP: %v", err)
	}
	names := []string{}
	for _, member := range unpacked.File {
		names = append(names, member.Name)
	}
	want := []string{"material/mat_1", "session/ses_1", "session/ses_2", ManifestName, DigestName}
	if len(names) != len(want) {
		t.Fatalf("the package holds %v, want %v", names, want)
	}
	for index := range want {
		if names[index] != want[index] {
			t.Errorf("member %d is %q, want %q", index, names[index], want[index])
		}
	}
	// The package is stored rather than deflated, which is what lets a reader
	// hash a member as it arrives without holding what it hashed.
	for _, member := range unpacked.File {
		if member.Method != zip.Store {
			t.Errorf("%s is compressed (method %d), want stored", member.Name, member.Method)
		}
	}
}

// ── helpers over ZIPs, for editing a package the writer would not have ──────

func zipOf(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, data := range members {
		member, err := archive.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := member.Write(data); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close the zip: %v", err)
	}
	return buffer.Bytes()
}

func rebuild(t *testing.T, pkg []byte, edit func(map[string][]byte)) []byte {
	t.Helper()
	unpacked, err := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		t.Fatalf("read the package: %v", err)
	}
	members := make(map[string][]byte, len(unpacked.File))
	order := make([]string, 0, len(unpacked.File))
	for _, member := range unpacked.File {
		opened, err := member.Open()
		if err != nil {
			t.Fatalf("open %s: %v", member.Name, err)
		}
		data, err := io.ReadAll(opened)
		opened.Close()
		if err != nil {
			t.Fatalf("read %s: %v", member.Name, err)
		}
		members[member.Name] = data
		order = append(order, member.Name)
	}
	edit(members)
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, name := range order {
		data, held := members[name]
		if !held {
			continue
		}
		member, err := archive.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := member.Write(data); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		delete(members, name)
	}
	for name, data := range members {
		member, err := archive.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := member.Write(data); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close the zip: %v", err)
	}
	return buffer.Bytes()
}

func rewriteMember(t *testing.T, pkg []byte, name string, edit func([]byte) []byte) []byte {
	t.Helper()
	return rebuild(t, pkg, func(members map[string][]byte) {
		members[name] = edit(members[name])
	})
}

func dropMember(t *testing.T, pkg []byte, name string) []byte {
	t.Helper()
	return rebuild(t, pkg, func(members map[string][]byte) {
		delete(members, name)
	})
}

func addMember(t *testing.T, pkg []byte, name string, data []byte) []byte {
	t.Helper()
	return rebuild(t, pkg, func(members map[string][]byte) {
		members[name] = data
	})
}
