package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"backend/internal/authz"
	"backend/internal/data/models"
	"backend/internal/kb"
	"backend/internal/notebook"
	"backend/internal/reading"
	"backend/internal/session"
	"backend/internal/turn"
	"backend/internal/workspace"
	"gorm.io/gorm"
)

// The four sinks between a package and the domains it is written back into.
//
// This is collect.go's mirror image at the assembly: `workspace` declares a Sink
// per kind and knows nothing about a session, a material, a question or a
// collection; each domain knows nothing about packages; and what an object *is*
// on its way back in is decided here and nowhere else. A reader who has
// understood the four sources has understood the four sinks.
//
// A payload is the domain's own projection, so the types below are the very ones
// workspacelayer.go encodes rather than a decode-side copy of them. That is what
// makes an export and an import in one build agree by construction: a field
// added to a payload is a field both directions see, and there is no second
// shape that can drift from the first.
//
// Three things every sink decides rather than takes from the package, and they
// are the whole of what an import rewrites about what it was handed:
//
//   - The id comes from the ref. The ref is what the manifest, the preview and
//     the recovery's record all name; a row written under an id out of the
//     payload's own field would be a row that recovery, which names refs, could
//     not take back.
//   - The owner comes from the caller. A package is an account's work arriving
//     at an instance, and the account it lands in is the one that asked. The
//     alternative — letting the package name its own owner — is an import that
//     writes rows into somebody else's account.
//   - What did not travel is not written. Each sink writes the columns the
//     domain's own write takes and leaves everything else to that domain's
//     defaults, because a column nobody packed is a column an import would be
//     inventing.
//
// What a sink may not rely on is the plan. A preview and a commit are two
// requests, and the row the plan said was absent may have arrived since. Every
// write here therefore answers a collision the way its domain answers one, and
// a refused batch is a failed and recoverable job rather than a silent
// overwrite (sink.go argues this at the port).

// The payloads, one per kind, and the same types the sources encode.
//
// They are named and shared rather than written twice because an anonymous
// struct written once for the encode and once for the decode is two shapes that
// only agree until somebody edits one of them — and the failure that buys is a
// package that an earlier build can write and a later build cannot read.
type sessionPayload struct {
	Session  session.Session   `json:"session"`
	Messages []session.Message `json:"messages"`
}

type materialPayload struct {
	Material reading.Material `json:"material"`
}

type questionPayload struct {
	Entry notebook.Entry `json:"entry"`
}

type knowledgeBasePayload struct {
	KnowledgeBase kb.KnowledgeBase `json:"knowledge_base"`
	Documents     []kb.Document    `json:"documents"`
}

// decodePayload reads one object back into a payload.
//
// A failure here is a bad package and not a bad request: the manifest has
// already said these bytes are the bytes that were written, so bytes that do not
// decode are bytes this build did not write them from.
func decodePayload(object workspace.Object, into any) error {
	if err := json.Unmarshal(object.Data, into); err != nil {
		return fmt.Errorf("%w: %s could not be read back", workspace.ErrBadPackage, object.Ref)
	}
	return nil
}

// held answers whether one row of this account's is already here.
//
// Each domain answers an id it has nothing for with its own ErrNotFound, and for
// an id that was never issued and one that is another account's alike — which is
// AUTH-06, and the reason a "not found" here means "this import may write it"
// rather than "somebody else has it".
type held func(ctx context.Context, id string) (bool, error)

// planObjects answers one item per object: a conflict when the row is already
// here, a create when it is not.
//
// It never answers a miss. A miss is an object that needs something the package
// does not carry, and the objects a package is written from name library files,
// documents and materials — things that are not kinds and therefore cannot be in
// one. A question's material_id is a stored file's id, a document is nothing
// without the file it was parsed from, and neither is something a ref can be
// held for. The action stays in the closed set and validated by plan.go; it is
// this build's sinks that have no case to report it from, which is a divergence
// from §7.3's eighth item and is reported rather than papered over.
func planObjects(ctx context.Context, objects []workspace.Object, alreadyThere held) ([]workspace.Item, error) {
	items := make([]workspace.Item, 0, len(objects))
	for _, object := range objects {
		there, err := alreadyThere(ctx, object.ID)
		if err != nil {
			return nil, err
		}
		if there {
			items = append(items, workspace.Item{
				Ref: object.Ref, Action: workspace.ActionConflict, Reason: workspace.ReasonAlreadyPresent,
			})
			continue
		}
		items = append(items, workspace.Item{Ref: object.Ref, Action: workspace.ActionCreate})
	}
	return items, nil
}

// written records one row that landed, which is what a recovery is driven by.
func written(object workspace.Object) workspace.Written {
	return workspace.Written{Kind: object.Kind, ID: object.ID}
}

// gone is every undo's tolerance: a row that is not there is a row this recovery
// cannot remove twice, and Sink.Undo calls the two answers the same on purpose.
func gone(err error, sentinel error) bool { return err == nil || errors.Is(err, sentinel) }

// sessionSink writes conversations and the messages in them.
//
// The session is one row and the messages are its children, so the session is
// recorded as landed the moment its own row exists: the write of a message can
// fail, and a session with half its messages is still a session a recovery can
// take away — and one it must, because leaving it would leave a conversation
// nobody asked for holding an id the package names.
type sessionSink struct {
	store session.Repository
}

func (s sessionSink) Plan(ctx context.Context, caller authz.Caller, objects []workspace.Object) ([]workspace.Item, error) {
	return planObjects(ctx, objects, func(ctx context.Context, id string) (bool, error) {
		_, err := s.store.Session(ctx, id, caller)
		if errors.Is(err, session.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s sessionSink) Write(ctx context.Context, caller authz.Caller, objects []workspace.Object) ([]workspace.Written, error) {
	landed := make([]workspace.Written, 0, len(objects))
	for _, object := range objects {
		var payload sessionPayload
		if err := decodePayload(object, &payload); err != nil {
			return landed, err
		}
		record := payload.Session
		record.ID, record.OwnerID = object.ID, caller.UserID
		if err := s.store.Create(ctx, record); err != nil {
			return landed, err
		}
		landed = append(landed, written(object))
		// A conversation that was in the recycle bin when it was packed goes back
		// into it: the bin is a state of the session rather than a row somewhere
		// else, and an import that dropped it would hand back a conversation
		// somebody had put away. Create writes a live row, so the state is
		// applied after it rather than in it.
		if record.Deleted() {
			if err := s.store.SetDeleted(ctx, object.ID, true, record.DeletedAt, caller); err != nil {
				return landed, err
			}
		}
		for _, message := range payload.Messages {
			message.SessionID = object.ID
			if err := s.store.AppendMessage(ctx, message, caller); err != nil {
				return landed, err
			}
		}
	}
	return landed, nil
}

func (s sessionSink) Undo(ctx context.Context, caller authz.Caller, rows []workspace.Written) error {
	for _, row := range rows {
		// Delete is one statement and the schema's foreign keys take the messages
		// with it, which is why no message id is recorded anywhere: what a
		// recovery names is the row it created, and everything under it follows.
		if err := s.store.Delete(ctx, row.ID, caller); !gone(err, session.ErrNotFound) {
			return err
		}
	}
	return nil
}

// materialSink writes reading materials.
//
// It is the material's row and nothing else. A material reads a document, the
// document was parsed from a library file, and the file's bytes are this
// instance's — none of which a package carries. The row that lands therefore
// names a content id an instance may have nothing for, which is the dependency
// §2.13 asks the preview to report and this build's preview cannot: see
// planObjects.
type materialSink struct {
	store reading.Repository
}

func (s materialSink) Plan(ctx context.Context, caller authz.Caller, objects []workspace.Object) ([]workspace.Item, error) {
	return planObjects(ctx, objects, func(ctx context.Context, id string) (bool, error) {
		_, err := s.store.Material(ctx, id, caller)
		if errors.Is(err, reading.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s materialSink) Write(ctx context.Context, caller authz.Caller, objects []workspace.Object) ([]workspace.Written, error) {
	landed := make([]workspace.Written, 0, len(objects))
	for _, object := range objects {
		var payload materialPayload
		if err := decodePayload(object, &payload); err != nil {
			return landed, err
		}
		record := payload.Material
		record.MaterialID, record.OwnerID = object.ID, caller.UserID
		if err := s.store.CreateMaterial(ctx, record); err != nil {
			return landed, err
		}
		landed = append(landed, written(object))
	}
	return landed, nil
}

func (s materialSink) Undo(ctx context.Context, caller authz.Caller, rows []workspace.Written) error {
	for _, row := range rows {
		// One statement: the positions, marks, pairings and workspace tabs that
		// hang off a material are the schema's children of it (00004, 00012), so
		// taking the material away takes them.
		if err := s.store.DeleteMaterial(ctx, row.ID, caller); !gone(err, reading.ErrNotFound) {
			return err
		}
	}
	return nil
}

// questionSink writes questions into the bank.
//
// It goes through the service rather than the repository, and that is the one
// place this file departs from "the domain's own write". A question's identity
// is the content address of its text and its type, and the address is computed
// rather than taken from a caller (service.go says why). A sink that inserted
// the row itself would be a second place that decides what a question is called,
// and the first package whose text and address disagreed would be filed where
// nobody could find it again.
//
// What that costs is one check, and it is made before anything is written: the
// address the bank would compute has to be the address the package filed the
// object under. If it is not, the package is asking for a row at an id its own
// content does not hash to — a package this build did not write from that text,
// and one whose recovery, which names the ref, could take nothing back. Asking
// the question first rather than inspecting the answer afterwards is the whole
// difference between a refusal and a refusal with a stray question left in the
// bank: `Save` writes the row under the address it computed, and an id nobody
// can name is an id no recovery can remove.
type questionSink struct {
	service *notebook.Service
	store   notebook.Repository
}

func (s questionSink) Plan(ctx context.Context, caller authz.Caller, objects []workspace.Object) ([]workspace.Item, error) {
	return planObjects(ctx, objects, func(ctx context.Context, id string) (bool, error) {
		_, err := s.store.Entry(ctx, id, caller)
		if errors.Is(err, notebook.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s questionSink) Write(ctx context.Context, caller authz.Caller, objects []workspace.Object) ([]workspace.Written, error) {
	landed := make([]workspace.Written, 0, len(objects))
	for _, object := range objects {
		var payload questionPayload
		if err := decodePayload(object, &payload); err != nil {
			return landed, err
		}
		question := payload.Entry.Question
		question.Categories = payload.Entry.Categories
		// The bank's own address rule, asked before the write. It is the same
		// function Save calls, so this is not a second opinion about what a
		// question is called — it is that opinion applied before there is a row
		// to leave behind.
		if address := notebook.QuestionID(question.Text, question.Type); address != object.ID {
			return landed, fmt.Errorf("%w: %s is the address of other text", workspace.ErrBadPackage, object.Ref)
		}
		saved, err := s.service.Save(ctx, question, caller)
		if err != nil {
			return landed, err
		}
		landed = append(landed, written(object))

		// The two marks the bank's own write can express travel with the
		// question: whether it was bookmarked and what the reader decided about
		// it are the user's own filing, and a notebook that arrived without them
		// would have lost the part of it somebody made by hand.
		//
		// The rest of the answer face does not travel, and it is a real gap: what
		// was answered, whether it was right and how many times are written by the
		// review path rather than by the bank, and the service has no call that
		// sets them. A question imported here arrives unanswered, which is
		// reported rather than hidden.
		marks := notebook.Change{}
		if payload.Entry.Answer.Bookmarked {
			bookmarked := true
			marks.Bookmarked = &bookmarked
		}
		if payload.Entry.Answer.Resolved != nil {
			resolved := *payload.Entry.Answer.Resolved
			marks.Resolved = &resolved
		}
		if marks.Empty() {
			continue
		}
		if err := s.service.Mark(ctx, saved.QuestionID, marks, caller); err != nil {
			return landed, err
		}
	}
	return landed, nil
}

func (s questionSink) Undo(ctx context.Context, caller authz.Caller, rows []workspace.Written) error {
	for _, row := range rows {
		// The question's address rather than the row key Save invented, because
		// the address is what this record and the package both name.
		if err := s.store.DeleteQuestion(ctx, row.ID, caller); !gone(err, notebook.ErrNotFound) {
			return err
		}
	}
	return nil
}

// knowledgeBaseSink writes collections and the catalogue of what is in them.
//
// What travels is the collection and its documents' metadata; what does not is
// the text, the chunks and the vectors. A vector is a function of a file's text
// and an embedding model, so a package carrying one would be claiming to have
// run somebody else's model over somebody else's file. A document therefore
// lands with no text and its vectors pending, which is the state the indexer
// already knows how to pick up — and it picks it up from the file, which is the
// one thing a collection's objects cannot bring with them. A collection whose
// files are not on this instance fails the commit rather than importing
// documents that point at nothing: documents.file_id is NOT NULL with a foreign
// key to library_files (00007), so the database is the thing that decides it and
// this file does not have to guess.
type knowledgeBaseSink struct {
	store kb.Repository
}

func (s knowledgeBaseSink) Plan(ctx context.Context, caller authz.Caller, objects []workspace.Object) ([]workspace.Item, error) {
	return planObjects(ctx, objects, func(ctx context.Context, id string) (bool, error) {
		_, err := s.store.KnowledgeBase(ctx, id, caller)
		if errors.Is(err, kb.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s knowledgeBaseSink) Write(ctx context.Context, caller authz.Caller, objects []workspace.Object) ([]workspace.Written, error) {
	landed := make([]workspace.Written, 0, len(objects))
	for _, object := range objects {
		var payload knowledgeBasePayload
		if err := decodePayload(object, &payload); err != nil {
			return landed, err
		}
		record := payload.KnowledgeBase
		record.ID, record.OwnerID = object.ID, caller.UserID
		// Documents and ReadyDocuments are counts of what is in the base, read
		// for the listing rather than stored on this row: a package's copy of
		// them would be a number about the instance it came from.
		record.Documents, record.ReadyDocuments = 0, 0
		// Whether this base is this instance's default is decided in storage —
		// the first base an account has is the default and the unique index on
		// the marker decides a race (repository.go). A package is not a place a
		// mark like that can come from.
		record.IsDefault = false
		if err := s.store.CreateKnowledgeBase(ctx, record); err != nil {
			return landed, err
		}
		landed = append(landed, written(object))
		for _, document := range payload.Documents {
			if err := s.store.CreateDocument(ctx, documentWrite(object.ID, document)); err != nil {
				return landed, err
			}
		}
	}
	return landed, nil
}

// documentWrite is one document of a package as the row to insert.
//
// TextChars is set to what the text is, and the text is empty: the comment on
// kb.DocumentWrite says the count is what a locator is clamped against, so a row
// that kept the count it was packed with would let every window read run off the
// end of a document holding nothing. VectorState is pending for the same reason
// from the other side — nothing here has embedded anything.
func documentWrite(kbID string, document kb.Document) kb.DocumentWrite {
	document.KBID = kbID
	document.TextChars = 0
	document.VectorState = kb.VectorStatePending
	document.VectorError = ""
	return kb.DocumentWrite{Document: document}
}

func (s knowledgeBaseSink) Undo(ctx context.Context, caller authz.Caller, rows []workspace.Written) error {
	for _, row := range rows {
		// The documents are the base's children by the foreign key in 00007, so
		// one statement takes the base and everything the import put in it.
		if err := s.store.DeleteKnowledgeBase(ctx, row.ID, caller); !gone(err, kb.ErrNotFound) {
			return err
		}
	}
	return nil
}

// runningTurns is the instance's answer to "is anything in flight" (AC-09).
//
// It is a count of the rows in the three active statuses rather than the
// runtime's own in-process count, and the difference matters here: a job may be
// answered by a process that is not the one running the turn, so a number held
// in a runner's memory would be a number that is right on one instance and zero
// on the rest. The three names are spelled out below rather than read from turn's
// own list, which is unexported; turn.Status.Active exists for the runtime's
// use, and this is a query rather than a decision about one turn.
type runningTurns struct {
	db *gorm.DB
}

var _ workspace.Busy = runningTurns{}

func (r runningTurns) Running(ctx context.Context) (int64, error) {
	var count int64
	active := []string{
		string(turn.StatusQueued), string(turn.StatusRunning), string(turn.StatusWaitingInput),
	}
	err := r.db.WithContext(ctx).Model(&models.Turn{}).Where("status IN ?", active).Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count the turns in flight: %w", err)
	}
	return count, nil
}
