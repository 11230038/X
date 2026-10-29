package handler

// The two directions of the reading wire: the views a route answers with, and
// the bodies it accepts. They are here rather than beside the routes because
// they are the contract a client is written against, and a route is free to
// change while the shape of the answer is not.
//
// The routes themselves are reading_handlers.go.

import (
	"time"

	"backend/internal/reading"
)

// materialView is one material as a client reads it.
//
// It carries no text and no document: a material is a row pointing at a
// document, and the words are read through the outline and the content route,
// one unit at a time (§2.8).
type materialView struct {
	MaterialID string    `json:"material_id"`
	ContentID  string    `json:"content_id"`
	Filename   string    `json:"filename"`
	SourceType string    `json:"source_type,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	// Pinned and Opened are the workspace tab's state, and they are absent when
	// the catalogue was read outside a workspace — absent says nobody has said,
	// which is not the same as false saying nobody has.
	Pinned *bool `json:"pinned,omitempty"`
	Opened *bool `json:"opened,omitempty"`
}

// outlineView is one material and what it is made of.
//
// The units are the domain's own type rather than a second struct with the same
// fields. Its JSON is a contract — a client holds a unit's ordinal and span and
// sends them back as a locator — so a view beside it would be a second
// definition of the same coordinates, free to drift.
type outlineView struct {
	Material  materialView   `json:"material"`
	Units     []reading.Unit `json:"units"`
	TextChars int64          `json:"text_chars"`
	// Position is where this session stopped, and it is here rather than behind
	// a route of its own because opening a material is one question — "what is
	// this and where was I" — and a read that answered half of it would cost a
	// second round trip before the first screen could be drawn. It is absent
	// when the client named no session, which is a catalogue read.
	Position *positionView `json:"position,omitempty"`
}

// passageView is one span of text and the coordinate it was read at.
//
// The locator that comes back is the one that was resolved rather than the one
// that was asked for: a client that sent a stale coordinate is shown where the
// text it got actually lives.
type passageView struct {
	MaterialID string          `json:"material_id"`
	Locator    reading.Locator `json:"locator"`
	Text       string          `json:"text"`
}

// positionView is where one reader stopped in one material.
type positionView struct {
	MaterialID string          `json:"material_id"`
	SessionID  string          `json:"session_id"`
	Locator    reading.Locator `json:"locator"`
	Revision   int64           `json:"revision"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// markView is one thing a reader wrote in the margin.
type markView struct {
	MarkID     string           `json:"mark_id"`
	MaterialID string           `json:"material_id"`
	SessionID  string           `json:"session_id,omitempty"`
	Kind       reading.MarkKind `json:"kind"`
	Locator    reading.Locator  `json:"locator"`
	Body       string           `json:"body,omitempty"`
	Revision   int64            `json:"revision"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

// bookmarkView is one saved place, with the material it was saved in and the
// words it covers.
//
// The material is a name rather than a whole catalogue row: what a favourites
// card says is which book this came from, and the row's other columns would be
// a second copy of the catalogue's answer.
type bookmarkView struct {
	Mark     markView             `json:"mark"`
	Material bookmarkMaterialView `json:"material"`
	Text     string               `json:"text,omitempty"`
}

type bookmarkMaterialView struct {
	MaterialID string `json:"material_id"`
	Filename   string `json:"filename"`
}

type createMaterialRequest struct {
	FileID     string `json:"file_id"`
	SourceType string `json:"source_type"`
}

type savePositionRequest struct {
	SessionID string          `json:"session_id"`
	Locator   reading.Locator `json:"locator"`
	// Revision is the revision the client read, and zero means it read none —
	// that is a request to create the position rather than to move one (§2.11).
	Revision int64 `json:"revision"`
}

type addMarkRequest struct {
	SessionID string          `json:"session_id"`
	Kind      string          `json:"kind"`
	Locator   reading.Locator `json:"locator"`
	Body      string          `json:"body"`
}

type runExtensionRequest struct {
	// Locator says which passage the extension is about. The text itself is not
	// on the wire: it is resolved here, server-side, which is what keeps what an
	// extension is handed this service's answer rather than the client's (§2.9).
	Locator reading.Locator `json:"locator"`
}

func toMaterialView(record reading.Material) materialView {
	return materialView{
		MaterialID: record.MaterialID, ContentID: record.ContentID,
		Filename: record.Filename, SourceType: record.SourceType,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		Pinned: record.Pinned, Opened: record.Opened,
	}
}

func toPositionView(record reading.Position) positionView {
	return positionView{
		MaterialID: record.MaterialID, SessionID: record.SessionID,
		Locator: record.Locator, Revision: record.Revision,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func toBookmarkView(bookmark reading.Bookmark) bookmarkView {
	return bookmarkView{
		Mark: toMarkView(bookmark.Mark),
		Material: bookmarkMaterialView{
			MaterialID: bookmark.Material.MaterialID,
			Filename:   bookmark.Material.Filename,
		},
		Text: bookmark.Text,
	}
}

func toMarkView(record reading.Mark) markView {
	return markView{
		MarkID: record.MarkID, MaterialID: record.MaterialID, SessionID: record.SessionID,
		Kind: record.Kind, Locator: record.Locator, Body: record.Body,
		Revision: record.Revision, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}
