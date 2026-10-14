package main

import (
	"context"
	"encoding/json"
	"fmt"

	"backend/internal/audit"
	"backend/internal/authz"
	"backend/internal/config"
	"backend/internal/kb"
	"backend/internal/notebook"
	"backend/internal/reading"
	"backend/internal/session"
	"backend/internal/workspace"
	"gorm.io/gorm"
)

// The four adapters between a package and the domains it holds.
//
// They are here for the reason fileSource is here (§1.2): the two sides are
// deliberately unaware of each other, and this is the one place that has both.
// `workspace` declares a Source per kind and knows nothing about sessions,
// materials, questions or collections; each domain knows nothing about packages;
// and what an object *is* on its way into one is decided in this file and
// nowhere else.
//
// A payload is the domain's own projection encoded, and not a second shape
// invented for the wire. An export and an import in the same build therefore
// agree by construction, and the only way to change what a package holds is to
// change what the domain says — which is the property a hand-written export DTO
// would spend its whole life quietly losing.
//
// What is not here is the point of §12.5: there is no adapter for an account
// setting, a Memory, a credential or a call log, and no kind for one to be
// collected under.

// workspaceSessions packs a conversation and its messages.
//
// The messages are read as the account rather than as the session, so a
// conversation that changed hands cannot be packed out by somebody who can name
// its id. That is the same rule every read in this repository follows, applied
// where it is easiest to forget: the ref is a string from a request body.
type workspaceSessions struct {
	service *session.Service
}

func (s workspaceSessions) Objects(ctx context.Context, caller authz.Caller, refs []workspace.Ref) ([]workspace.Object, error) {
	objects := make([]workspace.Object, 0, len(refs))
	for _, ref := range refs {
		record, err := s.service.Get(ctx, ref.ID, caller)
		if err != nil {
			return nil, err
		}
		messages, err := s.service.Messages(ctx, ref.ID, caller)
		if err != nil {
			return nil, err
		}
		data, err := encodePayload(sessionPayload{Session: record, Messages: messages})
		if err != nil {
			return nil, err
		}
		objects = append(objects, workspace.Object{Ref: ref, Data: data})
	}
	return objects, nil
}

// workspaceMaterials packs a reading material.
//
// It is the material and not the text it reads. The text belongs to a document,
// the document was parsed from a library file, and the file's bytes are the
// library's — so what travels is the material, and a package opened on another
// instance names a file that instance does not have. That is not a gap to be
// closed here: §2.13 asks the import preview to *report* a dependency the
// package does not carry, and this is the example it gives.
type workspaceMaterials struct {
	service *reading.Service
}

func (m workspaceMaterials) Objects(ctx context.Context, caller authz.Caller, refs []workspace.Ref) ([]workspace.Object, error) {
	objects := make([]workspace.Object, 0, len(refs))
	for _, ref := range refs {
		record, err := m.service.Material(ctx, ref.ID, caller)
		if err != nil {
			return nil, err
		}
		data, err := encodePayload(materialPayload{Material: record})
		if err != nil {
			return nil, err
		}
		objects = append(objects, workspace.Object{Ref: ref, Data: data})
	}
	return objects, nil
}

// workspaceQuestions packs one question with its answer and its categories.
//
// A question is addressed by its content address rather than by its row, and the
// address is what travels: the row's own key is this database's, and a package
// that carried it would be a package that claims to know what the next
// instance's key will be.
type workspaceQuestions struct {
	service *notebook.Service
}

func (q workspaceQuestions) Objects(ctx context.Context, caller authz.Caller, refs []workspace.Ref) ([]workspace.Object, error) {
	objects := make([]workspace.Object, 0, len(refs))
	for _, ref := range refs {
		entry, err := q.service.Get(ctx, ref.ID, caller)
		if err != nil {
			return nil, err
		}
		data, err := encodePayload(questionPayload{Entry: entry})
		if err != nil {
			return nil, err
		}
		objects = append(objects, workspace.Object{Ref: ref, Data: data})
	}
	return objects, nil
}

// workspaceKnowledgeBases packs a collection and its documents' metadata.
//
// The chunks and their vectors are left behind, and they are left behind on
// purpose. A vector is a function of a file's text and an embedding model, so a
// package carrying them would be claiming to have run somebody else's model on
// somebody else's file — and the instance that opens it rebuilds them with the
// indexer it already has, out of text it may not have. What is carried is the
// catalogue: what was in the collection and what it was parsed from.
type workspaceKnowledgeBases struct {
	service *kb.Service
}

func (c workspaceKnowledgeBases) Objects(ctx context.Context, caller authz.Caller, refs []workspace.Ref) ([]workspace.Object, error) {
	objects := make([]workspace.Object, 0, len(refs))
	for _, ref := range refs {
		record, err := c.service.KnowledgeBase(ctx, ref.ID, caller)
		if err != nil {
			return nil, err
		}
		documents, err := c.service.Documents(ctx, ref.ID, caller)
		if err != nil {
			return nil, err
		}
		data, err := encodePayload(knowledgeBasePayload{KnowledgeBase: record, Documents: documents})
		if err != nil {
			return nil, err
		}
		objects = append(objects, workspace.Object{Ref: ref, Data: data})
	}
	return objects, nil
}

// encodePayload renders one object.
//
// The error names the kind of thing that could not be encoded rather than the
// value, for the reason every error in this process does: the value is somebody's
// work and the message may be read by somebody else (§13.3).
func encodePayload(payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode an object for the package: %w", err)
	}
	return data, nil
}

// workspaceStores are the repositories the four sinks write through.
//
// They are the domains' own storage adapters rather than a second set of writes
// invented beside them, and they are handed here rather than reached for inside
// a service because two of the four things an import has to be able to do —
// taking a material or a question back — are repository methods with no service
// call around them (reading.Repository.DeleteMaterial, notebook.Repository.
// DeleteQuestion: their one caller is this recovery, §2.13).
type workspaceStores struct {
	sessions  session.Repository
	materials reading.Repository
	questions notebook.Repository
	documents kb.Repository
}

// newWorkspace builds the package service over the four sources and the four
// sinks.
//
// It is built last among the domains, and only after the five it reads exist:
// the reading layer and the collection service are the ones a material and a
// collection come from, and the sessions arrive as a parameter because the file
// library needed them before this function ran.
//
// Both directions are required, even of a deployment that only exports: a
// service with half a feature is a wiring mistake, and this is the last place
// the mistake is still a startup error rather than a route that answers 500
// (§12.5).
func newWorkspace(
	cfg config.Config,
	db *gorm.DB,
	sessions *session.Service,
	materials *kb.Service,
	questions *notebook.Service,
	immersive *readingLayer,
	auditLog *audit.Service,
	stores workspaceStores,
) (*workspace.Service, error) {
	sources, err := workspace.NewSources(map[workspace.Kind]workspace.Source{
		workspace.KindSession:       workspaceSessions{service: sessions},
		workspace.KindMaterial:      workspaceMaterials{service: immersive.service},
		workspace.KindQuestion:      workspaceQuestions{service: questions},
		workspace.KindKnowledgeBase: workspaceKnowledgeBases{service: materials},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize the package sources: %w", err)
	}
	sinks, err := workspace.NewSinks(map[workspace.Kind]workspace.Sink{
		workspace.KindSession:       sessionSink{store: stores.sessions},
		workspace.KindMaterial:      materialSink{store: stores.materials},
		workspace.KindQuestion:      questionSink{service: questions, store: stores.questions},
		workspace.KindKnowledgeBase: knowledgeBaseSink{store: stores.documents},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize the package sinks: %w", err)
	}
	service, err := workspace.NewService(workspace.Options{
		Root:            cfg.Workspace.ArchiveRoot,
		MaxArchiveBytes: cfg.Workspace.MaxArchiveBytes,
		JobTimeout:      cfg.Workspace.JobTimeout,
		Repository:      workspace.NewGORMRepository(db),
		Collector:       sources,
		Writer:          sinks,
		Busy:            runningTurns{db: db},
		Auditor:         auditLog,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize the package service: %w", err)
	}
	return service, nil
}
