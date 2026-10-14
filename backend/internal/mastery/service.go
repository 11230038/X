package mastery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The prefixes of the row ids this package makes. They are readable on purpose:
// a path id turns up in `notebook_entries.mastery_path_id` and in a URL, and a
// knowledge point id turns up inside a state document that somebody will
// eventually read by eye.
const (
	pathIDPrefix           = "mp_"
	knowledgePointIDPrefix = "kp_"
	eventIDPrefix          = "mev_"
	evidenceIDPrefix       = "mle_"
	idRandomBytes          = 16
)

// Service owns the rules about a mastery path: what may be changed, what a
// change has to leave behind, and what a version guard means when it fails.
type Service struct {
	repository Repository
	// clock is a field and not a call to time.Now inside the methods, because
	// every write here stores a time and a case that cannot choose the time
	// cannot pin the row it wrote.
	clock func() time.Time
}

// NewService returns the service over a storage adapter.
func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("mastery service needs a repository")
	}
	return &Service{repository: repository, clock: func() time.Time { return time.Now().UTC() }}, nil
}

// View is a path as a reader wants it: the tree, the version, and what the path
// is for.
type View struct {
	Path Path
	Meta TopicMeta
}

// Create is a request to start a path.
//
// Tree is the tree to start with, and an empty one is the ordinary case: a path
// created from a goal begins as a goal and is built by the turn that follows,
// one `mastery_map` call at a time. A path created from a material may arrive
// with modules already in it, which is why this is a field rather than "always
// empty".
type Create struct {
	PathID string
	Goal   string
	// Description and Emoji are the topic metadata's other two fields; an empty
	// emoji is stored as the reference implementation's compass.
	Description string
	Emoji       string
	Tree        Tree
	Actor       Actor
}

// Load reads a path and its topic metadata.
//
// A path with no metadata row is not an error: the row is written by Create, and
// a path that predates this version — the tables have been there since migration
// 00004 — has the tree and nothing else. Reporting what is there is more useful
// than refusing to report the tree because a display field is missing.
func (s *Service) Load(ctx context.Context, pathID string) (View, error) {
	path, err := s.repository.Path(ctx, pathID)
	if err != nil {
		return View{}, err
	}
	meta, err := s.repository.Meta(ctx, pathID)
	if err != nil {
		if !isNotFound(err) {
			return View{}, err
		}
		meta = TopicMeta{PathID: pathID, Status: StatusActive}
	}
	return View{Path: path, Meta: meta}, nil
}

// Create starts a path, its topic metadata and its history in one transaction.
//
// The three go together and the transaction is why: a path whose creation event
// was lost is a path that cannot say where it came from, and the event log is
// the only place that answer lives. What is *not* here is any notion of "the
// user's current path" — the design has no such thing, and a path is reached by
// its id.
func (s *Service) Create(ctx context.Context, create Create) (Path, error) {
	if err := create.Actor.Validate(); err != nil {
		return Path{}, err
	}
	if strings.TrimSpace(create.PathID) == "" {
		return Path{}, fmt.Errorf("%w: the path id is blank", ErrInvalidRequest)
	}
	if err := create.Tree.Validate(); err != nil {
		return Path{}, err
	}
	now := s.clock()
	path := Path{
		ID:             create.PathID,
		OwnerSessionID: create.Actor.SessionID,
		Tree:           create.Tree,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	emoji := create.Emoji
	if strings.TrimSpace(emoji) == "" {
		emoji = defaultEmoji
	}
	meta := TopicMeta{
		PathID:      create.PathID,
		Goal:        create.Goal,
		Description: create.Description,
		Emoji:       emoji,
		Status:      StatusActive,
	}

	err := s.repository.Within(ctx, func(repository Repository) error {
		if err := repository.CreatePath(ctx, path); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"goal":  meta.Goal,
			"emoji": meta.Emoji,
		})
		if err != nil {
			return fmt.Errorf("encode the creation of path %s: %w", path.ID, err)
		}
		eventID, err := newID(eventIDPrefix)
		if err != nil {
			return err
		}
		event := Event{
			ID:        eventID,
			PathID:    path.ID,
			Type:      EventPathCreated,
			Version:   path.Version,
			Payload:   payload,
			Actor:     create.Actor,
			CreatedAt: now,
		}
		if err := repository.AppendEvent(ctx, event); err != nil {
			return err
		}
		if err := repository.SaveMeta(ctx, meta, now); err != nil {
			return err
		}
		return repository.TouchSession(ctx, path.ID, create.Actor, now)
	})
	if err != nil {
		return Path{}, err
	}
	return path, nil
}

// defaultEmoji is what an unstated icon becomes. It is the reference
// implementation's own default for this column, and it is a constant rather than
// a `.env` key: it is a display default, not a deployment's preference.
const defaultEmoji = "🧭"

// Change is a request to change a path's tree.
//
// Expected is the version the caller read, and nil means "whichever version is
// on the row now" — a caller that has just read the path does not have to say so
// twice, and a caller that read it earlier and thought about it for a while does.
// Everything that changes a tree goes through change: the version guard, the
// event and the transaction are one rule and are written once.
type Change struct {
	PathID   string
	Actor    Actor
	Expected *int64
}

// AddKnowledgePointRequest is a request to add one knowledge point.
//
// Change carries the path, who is writing and the version the caller read; the
// rest says what to add. It is a struct of its own rather than a long parameter
// list because the two halves are read by different code — the guard by this
// package, the content by the caller — and because a positional call with five
// strings in it is a call that will be got wrong.
type AddKnowledgePointRequest struct {
	Change
	ModuleID string
	// ID may be blank, and then it is generated. A caller that has an id from
	// somewhere else — a material that already numbered its sections — may supply
	// one, and it is accepted as given.
	ID    string
	Name  string
	Type  string
	Notes string
}

// AddKnowledgePoint puts a new knowledge point into a module and returns the
// path as it now stands.
func (s *Service) AddKnowledgePoint(ctx context.Context, add AddKnowledgePointRequest) (Path, error) {
	id := strings.TrimSpace(add.ID)
	if id == "" {
		generated, err := newID(knowledgePointIDPrefix)
		if err != nil {
			return Path{}, err
		}
		id = generated
	}
	point := KnowledgePoint{
		ID:          id,
		Name:        add.Name,
		Type:        add.Type,
		ModuleID:    add.ModuleID,
		Description: add.Notes,
	}
	return s.change(ctx, add.Change, EventPathMapped, func(tree Tree) (Tree, []byte, error) {
		next, err := tree.WithKnowledgePoint(add.ModuleID, point)
		if err != nil {
			return Tree{}, nil, err
		}
		payload, err := json.Marshal(map[string]any{
			"change":          "knowledge_point_added",
			"module_id":       add.ModuleID,
			"knowledge_point": point,
		})
		if err != nil {
			return Tree{}, nil, fmt.Errorf("encode the addition of %s: %w", id, err)
		}
		return next, payload, nil
	})
}

// EditKnowledgePointRequest is a request to reword a knowledge point, to move
// it, or both.
//
// The two are one request because a caller that wants to say "this knowledge
// point belongs over there and is called something else" should be able to say
// it once, and because either half alone leaves the tree in a state nobody
// asked for. Position is only read when ToModule names a module.
type EditKnowledgePointRequest struct {
	Change
	ID   string
	Edit Edit
	// ToModule, when set, moves the knowledge point; Position is where in that
	// module, and a negative position appends.
	ToModule *string
	Position int
}

// EditKnowledgePoint applies a wording change and, optionally, a move.
func (s *Service) EditKnowledgePoint(ctx context.Context, edit EditKnowledgePointRequest) (Path, error) {
	if edit.Edit.Empty() && edit.ToModule == nil {
		return Path{}, fmt.Errorf("%w: nothing was named to change", ErrInvalidRequest)
	}
	return s.change(ctx, edit.Change, EventPathMapped, func(tree Tree) (Tree, []byte, error) {
		next := tree
		if !edit.Edit.Empty() {
			edited, err := next.EditKnowledgePoint(edit.ID, edit.Edit)
			if err != nil {
				return Tree{}, nil, err
			}
			next = edited
		}
		if edit.ToModule != nil {
			moved, err := next.MoveKnowledgePoint(edit.ID, *edit.ToModule, edit.Position)
			if err != nil {
				return Tree{}, nil, err
			}
			next = moved
		}
		payload, err := json.Marshal(map[string]any{
			"change":          "knowledge_point_edited",
			"knowledge_point": edit.ID,
			"module_id":       edit.ToModule,
		})
		if err != nil {
			return Tree{}, nil, fmt.Errorf("encode the edit of %s: %w", edit.ID, err)
		}
		return next, payload, nil
	})
}

// RemoveKnowledgePoint drops a knowledge point from the tree.
//
// The questions already filed against it are not touched, and that is the honest
// behaviour rather than an oversight: a question is an asset of the question
// bank (QN-01) and a coordinate into a tree that has changed is a coordinate
// that no longer resolves — which the insight endpoint is required to drop
// rather than report (§2.2, §8 第 12 条). Deleting the questions would be the
// other choice, and it would throw away work the learner did.
func (s *Service) RemoveKnowledgePoint(ctx context.Context, request RemoveKnowledgePointRequest) (Path, error) {
	return s.change(ctx, request.Change, EventPathMapped, func(tree Tree) (Tree, []byte, error) {
		next, err := tree.RemoveKnowledgePoint(request.ID)
		if err != nil {
			return Tree{}, nil, err
		}
		payload, err := json.Marshal(map[string]any{
			"change":          "knowledge_point_removed",
			"knowledge_point": request.ID,
		})
		if err != nil {
			return Tree{}, nil, fmt.Errorf("encode the removal of %s: %w", request.ID, err)
		}
		return next, payload, nil
	})
}

// RemoveKnowledgePointRequest is a request to drop one knowledge point.
type RemoveKnowledgePointRequest struct {
	Change
	ID string
}

// touch records that a conversation has used a path without changing it.
//
// It is not a Change: nothing about the tree moves, so no version is taken and
// no history is written — the design's history is about the tree, and "somebody
// looked at it" is not a fact about the tree. It is still a write, and it is
// still in a transaction, because a failed link between a path and a session
// would make the next turn in that session unable to find the path it was
// working on.
func (s *Service) Touch(ctx context.Context, pathID string, actor Actor) error {
	if err := actor.Validate(); err != nil {
		return err
	}
	return s.repository.Within(ctx, func(repository Repository) error {
		return repository.TouchSession(ctx, pathID, actor, s.clock())
	})
}

// change runs one change to a tree: read, change, write under the version guard,
// and leave the history behind — the last two in one transaction.
//
// The event is written before the state and it carries the version the state is
// about to reach. That order is the same one the review path takes (§2.1) and it
// has the same reason: the two are only consistent together, so the transaction
// is doing the work either way, and writing the explanation first means that a
// state which cannot be explained is not a state that can be stored.
func (s *Service) change(
	ctx context.Context,
	request Change,
	eventType EventType,
	mutate func(Tree) (Tree, []byte, error),
) (Path, error) {
	if err := request.Actor.Validate(); err != nil {
		return Path{}, err
	}
	now := s.clock()

	var updated Path
	err := s.repository.Within(ctx, func(repository Repository) error {
		path, err := repository.Path(ctx, request.PathID)
		if err != nil {
			return err
		}
		// The version the caller read outranks the one just read. Falling back to
		// the stored version would turn a caller that is behind into one that is up
		// to date, and the guard would then be guarding the server's state against
		// itself.
		expected := path.Version
		if request.Expected != nil {
			expected = *request.Expected
		}

		tree, payload, err := mutate(path.Tree)
		if err != nil {
			return err
		}
		if err := tree.Validate(); err != nil {
			return err
		}

		eventID, err := newID(eventIDPrefix)
		if err != nil {
			return err
		}
		event := Event{
			ID:        eventID,
			PathID:    path.ID,
			Type:      eventType,
			Version:   expected + 1,
			Payload:   payload,
			Actor:     request.Actor,
			CreatedAt: now,
		}
		if err := repository.AppendEvent(ctx, event); err != nil {
			return err
		}

		version, err := repository.SaveTree(ctx, path.ID, expected, tree, now)
		if err != nil {
			return err
		}
		if err := repository.TouchSession(ctx, path.ID, request.Actor, now); err != nil {
			return err
		}

		updated = path
		updated.Tree = tree
		updated.Version = version
		updated.UpdatedAt = now
		return nil
	})
	if err != nil {
		return Path{}, err
	}
	return updated, nil
}

// NewPathID returns an id for a path that does not exist yet.
//
// It is exported because the capability makes the id before it can call Create:
// the turn that creates a path announces the id in its first event, and an id
// invented twice would be two paths.
func NewPathID() (string, error) {
	return newID(pathIDPrefix)
}

func newID(prefix string) (string, error) {
	value := make([]byte, idRandomBytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(value), nil
}

// isNotFound reports whether an error is this package's absence sentinel.
func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
