package main

import (
	"context"

	"backend/internal/kb"
	"backend/internal/library"
)

// fileSource adapts the file library to the port the knowledge base reads stored
// files through.
//
// The two sides are deliberately unaware of each other: kb declares the port so
// that it never learns how bytes are stored, and library knows nothing about
// what a parsed document is. The conversion is here because this is the one
// place that has both, and it is field-for-field because the port was declared
// by looking at what a reader needs — the moment the two shapes drift, this is
// the line that stops compiling.
type fileSource struct {
	library *library.Service
}

var _ kb.FileSource = fileSource{}

// Open reads one stored file for parsing.
func (f fileSource) Open(ctx context.Context, fileID string) (kb.SourceFile, error) {
	file, err := f.library.Open(ctx, fileID)
	if err != nil {
		return kb.SourceFile{}, err
	}
	return kb.SourceFile{
		FileID:   file.ID,
		Filename: file.Filename,
		MIMEType: file.MIMEType,
		Category: file.Category,
		Content:  file.Content,
	}, nil
}
