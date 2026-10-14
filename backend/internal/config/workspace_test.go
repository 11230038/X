package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The workspace section's three keys (§5, §2.13).
//
// They are one root and two bounds, and what is worth pinning here is that
// neither bound may be zero: an archive root that is blank is a directory
// nobody named, a byte limit of zero refuses every package including the empty
// ones, and a job timeout of zero is a job that has already expired. None of
// the three is a value a deployment can mean.

func TestLoadReadsTheWorkspaceSection(t *testing.T) {
	values := validValues()
	values["WORKSPACE_MAX_ARCHIVE_BYTES"] = "1048576"
	values["WORKSPACE_JOB_TIMEOUT"] = "90s"
	path := writeDotenv(t, values)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Workspace.MaxArchiveBytes != 1048576 {
		t.Errorf("MaxArchiveBytes = %d, want 1048576", cfg.Workspace.MaxArchiveBytes)
	}
	// A duration, not a number of seconds: the key is read with time.ParseDuration
	// like every other duration in this file, so `15m` and `900s` are the same
	// value written two ways and neither is a bare number.
	if cfg.Workspace.JobTimeout != 90*time.Second {
		t.Errorf("JobTimeout = %s, want 90s", cfg.Workspace.JobTimeout)
	}
}

// TestLoadResolvesTheArchiveRootFromDotenvDirectory: the archive root is
// resolved against the file it was written in, the same rule UPLOAD_ROOT
// follows. It matters more here than there, because the shipped value is
// relative (`../data/archive`) and because a second place packages land —
// depending on who started the process — is a second place an import has to be
// able to find them.
func TestLoadResolvesTheArchiveRootFromDotenvDirectory(t *testing.T) {
	values := validValues()
	values["WORKSPACE_ARCHIVE_ROOT"] = "../runtime/archive"
	path := writeDotenv(t, values)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want, err := filepath.Abs(filepath.Join(filepath.Dir(path), "..", "runtime", "archive"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace.ArchiveRoot != want {
		t.Fatalf("ArchiveRoot = %q, want %q", cfg.Workspace.ArchiveRoot, want)
	}
}

// TestTheArchiveRootIsNotTheUploadRoot: the two are different directories by
// design (§2.13). A deployment that pointed them at one place would let a
// package be written where a user's material lives, and an import would then be
// able to name a file that is already somebody's.
//
// The configuration does not refuse the two being equal — they are read
// independently and it is a deployment's own mistake — so this is about the
// values the template ships: two keys whose defaults happen to resolve to one
// directory would be the mistake already made for everybody.
func TestTheArchiveRootIsNotTheUploadRoot(t *testing.T) {
	path := writeDotenv(t, validValues())

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.TrimSpace(cfg.Upload.Root) == "" || strings.TrimSpace(cfg.Workspace.ArchiveRoot) == "" {
		t.Fatalf("one of the roots is unresolved: upload=%q archive=%q",
			cfg.Upload.Root, cfg.Workspace.ArchiveRoot)
	}
	if cfg.Upload.Root == cfg.Workspace.ArchiveRoot {
		t.Fatalf("the template's two roots resolve to one directory (%q), which is the arrangement §2.13 forbids",
			cfg.Upload.Root)
	}
}

func TestLoadRejectsAWorkspaceSectionItCannotUse(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		value  string
		banned string
	}{
		{"a blank root", "WORKSPACE_ARCHIVE_ROOT", "   ", "WORKSPACE_ARCHIVE_ROOT"},
		{"a byte limit of zero", "WORKSPACE_MAX_ARCHIVE_BYTES", "0", "WORKSPACE_MAX_ARCHIVE_BYTES"},
		{"a negative byte limit", "WORKSPACE_MAX_ARCHIVE_BYTES", "-1", "WORKSPACE_MAX_ARCHIVE_BYTES"},
		{"a byte limit that is not a number", "WORKSPACE_MAX_ARCHIVE_BYTES", "512MB", "WORKSPACE_MAX_ARCHIVE_BYTES"},
		{"a timeout of zero", "WORKSPACE_JOB_TIMEOUT", "0s", "WORKSPACE_JOB_TIMEOUT"},
		{"a timeout with no unit", "WORKSPACE_JOB_TIMEOUT", "900", "WORKSPACE_JOB_TIMEOUT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := validValues()
			values[test.key] = test.value
			path := writeDotenv(t, values)

			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load() error = nil, want a refusal for %s=%s", test.key, test.value)
			}
			// The message names the key and never the value. A blank root is the
			// awkward one: "" in a message reads as "the value is nothing", which
			// is true and useless — the key is what the operator edits.
			if !strings.Contains(err.Error(), test.banned) {
				t.Errorf("error = %v, want it to name %s", err, test.banned)
			}
		})
	}
}

// TestAWorkspaceSectionWithNoDefaultsIsRefused: the three keys are required,
// like every other key in this file. Gone from the file is not "use the
// design's default" — it is a deployment that cannot start, which is the shape
// the whole configuration section has taken since V1.
func TestAWorkspaceSectionWithNoDefaultsIsRefused(t *testing.T) {
	for _, key := range []string{
		"WORKSPACE_ARCHIVE_ROOT", "WORKSPACE_MAX_ARCHIVE_BYTES", "WORKSPACE_JOB_TIMEOUT",
	} {
		t.Run(key, func(t *testing.T) {
			values := validValues()
			delete(values, key)
			path := writeDotenv(t, values)

			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load() error = nil, want a refusal with %s absent", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error = %v, want it to name %s", err, key)
			}
		})
	}
}
