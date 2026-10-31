package config

import (
	"fmt"
	"strings"
	"time"
)

// The workspace section (§5, §2.13).
//
// Three keys, and none of them is about what an export contains. What goes into
// a package is the shape of the feature — §12.5's list of what must not be in
// one is a product rule, not a knob — and the manifest's own format is a format
// any other implementation would have to read. So this section is only about
// where packages land, how big one may be, and how long a job may take.
//
// The archive root is a directory of its own rather than a corner of
// UPLOAD_ROOT, and the two are not interchangeable: UPLOAD_ROOT holds the
// library's *materials*, one file per library row, each of which a user may
// download. A package is a copy of many of those, plus metadata, and keeping it
// out of the upload tree is what stops an import from being able to overwrite a
// user's file by naming one.
//
// A key that is absent is a deployment that cannot start, as everywhere else in
// this package: the values below are the design's, and .env.example carries
// them, but no code supplies a default.

// WorkspaceConfig holds where packages are kept and the two bounds on a job.
type WorkspaceConfig struct {
	// ArchiveRoot is the directory package files are written to and read from.
	// It is resolved against the configuration file (see resolveConfigPath), the
	// same rule UPLOAD_ROOT follows and for the same reason.
	ArchiveRoot string
	// MaxArchiveBytes bounds one package. Import reads an uploaded file and
	// export writes one, and both go through this: a bound that only held on the
	// way in would be a bound on what somebody may send us rather than on what we
	// may write to our own disk.
	MaxArchiveBytes int64
	// JobTimeout is the wall-clock bound on one export or import. It exists
	// because a package can hold tens of thousands of rows and the work is done
	// in batches, so a job that has stopped making progress has to be able to
	// stop being a job.
	JobTimeout time.Duration
}

// Validate checks the root is named and the two bounds are bounds.
func (c WorkspaceConfig) Validate() error {
	if strings.TrimSpace(c.ArchiveRoot) == "" {
		return fmt.Errorf("WORKSPACE_ARCHIVE_ROOT must not be empty")
	}
	if c.MaxArchiveBytes <= 0 {
		return fmt.Errorf("WORKSPACE_MAX_ARCHIVE_BYTES must be a positive integer")
	}
	if c.JobTimeout <= 0 {
		return fmt.Errorf("WORKSPACE_JOB_TIMEOUT must be a positive duration")
	}
	return nil
}

func loadWorkspaceConfig(baseDir string, values map[string]string) (WorkspaceConfig, error) {
	root, err := resolveConfigPath(baseDir, values["WORKSPACE_ARCHIVE_ROOT"])
	if err != nil {
		return WorkspaceConfig{}, err
	}
	maxBytes, err := parsePositiveInt64(values, "WORKSPACE_MAX_ARCHIVE_BYTES")
	if err != nil {
		return WorkspaceConfig{}, err
	}
	timeout, err := parsePositiveDuration(values, "WORKSPACE_JOB_TIMEOUT")
	if err != nil {
		return WorkspaceConfig{}, err
	}
	return WorkspaceConfig{ArchiveRoot: root, MaxArchiveBytes: maxBytes, JobTimeout: timeout}, nil
}
