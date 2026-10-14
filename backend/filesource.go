package main

import (
	"context"

	"backend/internal/authz"
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

// Open reads one stored file for parsing, as the caller's own file.
//
// The account is passed through rather than dropped here, and this adapter is
// exactly where it would be easiest to drop: the port is about bytes, the
// library is about bytes, and the account looks like neither one's business. It
// is both of theirs. Whoever holds a file id could otherwise ask the knowledge
// base to parse somebody else's upload into their own collection and read it
// back as a document, which is the upload's contents by a longer road.
func (f fileSource) Open(ctx context.Context, fileID string, caller authz.Caller) (kb.SourceFile, error) {
	file, err := f.library.Open(ctx, fileID, caller)
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
