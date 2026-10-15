package workspace

// Taking a package out of the archive root, over a real file on a real disk.
//
// `Download` is a second way into the same file, and the thing worth proving is
// exactly that: that it opens the package the job's row names, that what it
// streams is that file byte for byte, and that the parsing way in still reads
// the same package — because a version of Download that resolved a path of its
// own would pass any case written against a reader the case supplied, and only a
// file on a disk says otherwise. The root below is `t.TempDir`, and the package
// in it is one this build's own export wrote.
//
// What is *not* here, and cannot be: that the bytes did not pass through a
// parsed copy of themselves on the way out. `workspace.Open` on a 512 MiB
// package streams the same bytes and only the process's memory would differ, so
// that half of §2.5 is a rule to be read rather than a case to be run — the
// download route's own test says the same thing where it is reached.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// exportAPackage makes a job the way a client does, and answers the job it made
// along with the bytes the archive root holds for it.
func exportAPackage(t *testing.T, app *harness) (Job, []byte) {
	t.Helper()
	job, err := app.service.Export(context.Background(), ExportRequest{
		Actor: exporter(), Refs: sampleRefs(),
	})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if job.State != StateCommitted {
		t.Fatalf("the export ended %s (%s), want committed", job.State, job.FailureCode)
	}
	onDisk, err := os.ReadFile(filepath.Join(app.root, job.ArchivePath))
	if err != nil {
		t.Fatalf("reading the package the export wrote: %v", err)
	}
	return job, onDisk
}

func TestDownloadStreamsThePackageTheJobNames(t *testing.T) {
	app := newHarness(t)
	job, onDisk := exportAPackage(t, app)

	stored, err := app.service.Download(context.Background(), job)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	defer stored.Content.Close()

	// The length is the file's, not a count of what was read: a client that is
	// told a length it then has to correct has been told about a download that is
	// not the one it is getting.
	if stored.SizeBytes != int64(len(onDisk)) {
		t.Errorf("SizeBytes = %d, want the package's %d", stored.SizeBytes, len(onDisk))
	}
	streamed, err := io.ReadAll(stored.Content)
	if err != nil {
		t.Fatalf("reading the stream: %v", err)
	}
	if !bytes.Equal(streamed, onDisk) {
		t.Errorf("the stream is %d bytes and the disk %d, and they differ",
			len(streamed), len(onDisk))
	}

	// And it is the same package, not merely the same bytes: the parsing way in
	// reads what the streaming way out handed over, so the two cannot be
	// resolving different files under the root.
	parsed, err := OpenArchiveBytes(streamed)
	if err != nil {
		t.Fatalf("the downloaded package does not parse: %v", err)
	}
	if !parsed.Report.Clean() {
		t.Errorf("the downloaded package does not match its manifest: %s", parsed.Report.Summary())
	}
	if len(parsed.Objects) != len(job.Manifest) {
		t.Errorf("the downloaded package holds %d objects, want the job's %d",
			len(parsed.Objects), len(job.Manifest))
	}
}

func TestDownloadRefusesAJobThatNamesNoPackage(t *testing.T) {
	app := newHarness(t)

	// A job in state `preview` — the row exists and the export never got as far
	// as writing anything, which is what a failed export leaves behind.
	_, err := app.service.Download(context.Background(), Job{
		ID: "job_previewed", Kind: JobImport, State: StatePreview, OwnerID: 7,
	})
	if !errors.Is(err, ErrBadPackage) {
		t.Errorf("Download() of a job with no package = %v, want %v", err, ErrBadPackage)
	}

	// Open answers the same thing, because both go through the same resolution:
	// this is the half that would catch the two drifting apart.
	if _, err := app.service.Open(context.Background(), Job{
		ID: "job_previewed", Kind: JobImport, State: StatePreview, OwnerID: 7,
	}); !errors.Is(err, ErrBadPackage) {
		t.Errorf("Open() of a job with no package = %v, want %v", err, ErrBadPackage)
	}
}

func TestDownloadRefusesAPackageThatIsNotThere(t *testing.T) {
	app := newHarness(t)

	// The row names a package and the disk has none: the export reported itself
	// committed and left nothing behind. That is not ErrBadPackage — nothing
	// about the package is known to be bad — and it is not answered as an empty
	// stream either, which is the answer that would look like a working download
	// of a package with nothing in it.
	_, err := app.service.Download(context.Background(), Job{
		ID: "job_gone", Kind: JobExport, State: StateCommitted, OwnerID: 7,
		ArchivePath: "job_gone.zip",
	})
	if err == nil {
		t.Fatal("Download() of a job whose package is missing = nil, want a refusal")
	}
	if errors.Is(err, ErrBadPackage) {
		t.Errorf("a missing package is answered as %v, want the deployment's own fault", err)
	}
}
