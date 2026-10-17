package book

import (
	"context"

	"backend/internal/authz"
)

// Repository is the storage seam. Every method narrows to the caller: a book is
// personal, and being an administrator is not a reason to read somebody else's.
type Repository interface {
	// List returns the caller's shelf, newest first, with chapter counts.
	List(ctx context.Context, caller authz.Caller) ([]Summary, error)
	// Get returns one book's metadata. SourceText is not selected here.
	Get(ctx context.Context, bookID string, caller authz.Caller) (Book, error)
	// SourceText reads the snapshot. It is a separate method so the shelf can
	// never accidentally drag a MEDIUMTEXT through a list query.
	SourceText(ctx context.Context, bookID string, caller authz.Caller) (string, error)
	// Chapters lists a book's outline in ordinal order, without bodies.
	Chapters(ctx context.Context, bookID string, caller authz.Caller) ([]Chapter, error)
	// Chapter returns one chapter with its body.
	Chapter(ctx context.Context, bookID string, ordinal int, caller authz.Caller) (Chapter, error)
	// Create inserts a book. Its chapters are written by ReplaceOutline.
	Create(ctx context.Context, book Book) error
	// Delete removes a book; chapters follow by cascade.
	Delete(ctx context.Context, bookID string, caller authz.Caller) error
	// ReplaceOutline increments the revision, clears progress and replaces every
	// chapter. It refuses once any chapter is ready, and returns the new revision.
	ReplaceOutline(ctx context.Context, bookID string, caller authz.Caller, confirmed bool, chapters []Chapter) (int, error)
	// SaveChapter writes a body only if the outline still has the revision the
	// generation started under. False means the guard did not match.
	SaveChapter(ctx context.Context, chapterID, bookID string, revision int, content string, caller authz.Caller) (bool, error)
	// FailChapter records a failed generation without erasing an old body.
	FailChapter(ctx context.Context, chapterID, bookID string, revision int, code string, caller authz.Caller) error
	// SaveProgress stores the last chapter the owner opened.
	SaveProgress(ctx context.Context, bookID string, ordinal int, caller authz.Caller) error
	// ReadyChapters returns generated chapters with bodies, in ordinal order.
	ReadyChapters(ctx context.Context, bookID string, caller authz.Caller) ([]Chapter, error)
}
