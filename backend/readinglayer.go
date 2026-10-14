package main

import (
	"context"
	"fmt"

	"backend/internal/authz"
	"backend/internal/config"
	"backend/internal/event"
	"backend/internal/kb"
	"backend/internal/llm"
	"backend/internal/reading"
	"backend/internal/turn"
	"gorm.io/gorm"
)

// readingLayer is the reading domain as this process holds it: the service a
// person reaches through the routes, the extensions the build was assembled
// with, and the runtime's own view of a reader's selection.
type readingLayer struct {
	service *reading.Service
	// repository is the layer's own storage, kept here because one caller needs
	// it rather than the service: a workspace import takes back the materials it
	// wrote by deleting their rows, and DeleteMaterial is a repository method
	// with no service call around it (§2.13 — nothing in the product removes a
	// material). It is the same handle the service was built over.
	repository reading.Repository // extensions is the closed set of reading aids this build has. It is a value
	// and not a package-level map, so which extensions exist is decided here with
	// every other dependency (§2.9).
	extensions *reading.Registry
	// selection is the same service seen through the runtime's vocabulary: a turn
	// that carries a selection reaches the reading layer through this, and learns
	// nothing about materials, locators or documents by doing so.
	selection turn.Readings
}

// newReadingLayer builds the reading domain over the knowledge base the turns
// already read through.
//
// One service over one port, and the port is the join: the reading layer reads a
// document's coordinates and one window of its text through the knowledge base's
// own service, so the extractor, the offsets and the unit of "a character" are
// V2's rather than a second reader's (§2.8).
func newReadingLayer(cfg config.Config, db *gorm.DB, materials *kb.Service, provider llm.Provider) (*readingLayer, error) {
	repository := reading.NewGORMRepository(db)
	service, err := reading.NewService(
		repository,
		readingDocuments{materials: materials},
		reading.Options{UnitMaxChars: cfg.Reading.UnitMaxChars},
	)
	if err != nil {
		return nil, fmt.Errorf("initialize the reading layer: %w", err)
	}
	// The extensions answer a reading aid, so they run on the reading answer's
	// budget and on the conversation model — the one that answers a person in
	// their own language, which is what an explanation and a gloss are. Which
	// model serves them is decided here rather than read from configuration
	// inside the package, the same way every other model in this process is.
	extensions, err := reading.NewRegistry(cfg.Reading.ExtensionTimeout,
		reading.NewExplain(provider, cfg.LLM.Model, cfg.Reading.TutorMaxTokens),
		reading.NewVocabulary(provider, cfg.LLM.Model, cfg.Reading.TutorMaxTokens),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize the reading extensions: %w", err)
	}
	return &readingLayer{
		service:    service,
		repository: repository,
		extensions: extensions,
		selection:  readingSelection{readings: service},
	}, nil
}

// readingDocuments is the knowledge base as the reading layer reaches it.
//
// It is an adapter and not an import for the reason §1.2 gives: the knowledge
// base is a peer domain, and a catalogue of reading coordinates has no business
// knowing what a collection is. What crosses is four reads, each of which is one
// method the knowledge base already has.
type readingDocuments struct {
	materials *kb.Service
}

var _ reading.Documents = readingDocuments{}

func (d readingDocuments) DocumentOfFile(ctx context.Context, fileID string, caller authz.Caller) (reading.Document, error) {
	document, err := d.materials.DocumentOfFile(ctx, fileID, caller)
	if err != nil {
		return reading.Document{}, err
	}
	return readingDocument(document), nil
}

func (d readingDocuments) Document(ctx context.Context, documentID string, caller authz.Caller) (reading.Document, error) {
	document, err := d.materials.Document(ctx, documentID, caller)
	if err != nil {
		return reading.Document{}, err
	}
	return readingDocument(document), nil
}

func (d readingDocuments) ChunkSpans(ctx context.Context, documentID string, caller authz.Caller) ([]reading.ChunkSpan, error) {
	spans, err := d.materials.ChunkSpans(ctx, documentID, caller)
	if err != nil {
		return nil, err
	}
	converted := make([]reading.ChunkSpan, 0, len(spans))
	for _, span := range spans {
		converted = append(converted, reading.ChunkSpan{
			CharStart: span.CharStart, CharEnd: span.CharEnd, Page: span.Page,
		})
	}
	return converted, nil
}

// Text reads one window of a document, and it reads exactly the window that was
// asked for: the reading layer has already checked the span against the
// document's length, so the clamping the window read does is a second answer to
// a question that has one.
func (d readingDocuments) Text(ctx context.Context, documentID string, charStart, charEnd int64, caller authz.Caller) (string, error) {
	window, err := d.materials.ReadWindow(ctx, documentID, charStart, charEnd-charStart, caller)
	if err != nil {
		return "", err
	}
	return window.Text, nil
}

func readingDocument(document kb.Document) reading.Document {
	return reading.Document{
		ID: document.ID, FileID: document.FileID, Filename: document.Filename,
		TextChars: document.TextChars, Revision: document.ActiveRevision,
	}
}

// readingSelection is the join between the runtime and the reading layer (§2.8).
//
// The two ends speak their own vocabularies and neither may learn the other's:
// the runtime knows a selection is a material, a coordinate and some text, and
// the reading layer knows a locator, a material row and a document. This is the
// one place both are in scope, and it is the same seam masteryPath is.
type readingSelection struct {
	readings *reading.Service
}

var _ turn.Readings = readingSelection{}

// Passage resolves a selection into the source a turn cites it as.
//
// What the model is given is what the coordinate resolves to, and the client's
// own copy of the text is not consulted: a stale tab and a document re-parsed
// underneath one both produce text that is no longer there, and the reading
// layer answers about the text that is.
//
// The source carries the file and the revision as well as the span, because a
// citation is opened by the file and the offset and a reader opening one on a
// document that has been rebuilt since can see that the coordinates are from the
// revision before (§2.8).
func (s readingSelection) Passage(ctx context.Context, selection turn.Selection, caller authz.Caller) (event.Source, error) {
	passage, err := s.readings.Resolve(ctx, selection.MaterialID, reading.Locator{
		MaterialID: selection.MaterialID,
		Ordinal:    selection.Locator.Ordinal,
		CharStart:  selection.Locator.CharStart,
		CharEnd:    selection.Locator.CharEnd,
		Page:       selection.Locator.Page,
	}, caller)
	if err != nil {
		return event.Source{}, err
	}
	return event.Source{
		DocumentID: passage.DocumentID,
		FileID:     passage.FileID,
		Title:      passage.Filename,
		Locator: event.Locator{
			Kind:      locatorKindWindow,
			Ordinal:   passage.Locator.Ordinal,
			CharStart: passage.Locator.CharStart,
			CharEnd:   passage.Locator.CharEnd,
			Page:      passage.Locator.Page,
		},
		Snippet:  passage.Text,
		Revision: passage.Revision,
	}, nil
}

// locatorKindWindow is what a source read as a span of a document says it is,
// and it is the same word a read_source window carries: the two are the same
// thing arrived at from two directions, and a client that opens one already
// knows how to open the other.
const locatorKindWindow = "window"
