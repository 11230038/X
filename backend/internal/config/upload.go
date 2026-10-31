package config

import (
	"fmt"
	"strings"
)

// multipartHeadroomBytes is what a multipart request spends on anything that is
// not the file itself — boundaries, part headers, other fields — and so is the
// gap the two upload limits must keep.
const multipartHeadroomBytes int64 = 64 * 1024

// UploadConfig contains local file-library storage limits and location.
type UploadConfig struct {
	Root            string
	MaxFileBytes    int64
	MaxRequestBytes int64
}

// Validate checks upload storage and request limits.
func (c UploadConfig) Validate() error {
	if strings.TrimSpace(c.Root) == "" {
		return fmt.Errorf("UPLOAD_ROOT must not be empty")
	}
	if c.MaxFileBytes <= 0 {
		return fmt.Errorf("UPLOAD_MAX_FILE_BYTES must be a positive integer")
	}
	if c.MaxRequestBytes <= 0 {
		return fmt.Errorf("UPLOAD_MAX_REQUEST_BYTES must be a positive integer")
	}
	if c.MaxRequestBytes < multipartHeadroomBytes || c.MaxFileBytes > c.MaxRequestBytes-multipartHeadroomBytes {
		return fmt.Errorf("UPLOAD_MAX_REQUEST_BYTES must exceed UPLOAD_MAX_FILE_BYTES by at least %d bytes", multipartHeadroomBytes)
	}
	return nil
}

func loadUploadConfig(baseDir string, values map[string]string) (UploadConfig, error) {
	root, err := resolveConfigPath(baseDir, values["UPLOAD_ROOT"])
	if err != nil {
		return UploadConfig{}, err
	}
	maxFile, err := parsePositiveInt64(values, "UPLOAD_MAX_FILE_BYTES")
	if err != nil {
		return UploadConfig{}, err
	}
	maxRequest, err := parsePositiveInt64(values, "UPLOAD_MAX_REQUEST_BYTES")
	if err != nil {
		return UploadConfig{}, err
	}
	return UploadConfig{Root: root, MaxFileBytes: maxFile, MaxRequestBytes: maxRequest}, nil
}
