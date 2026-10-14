package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"backend/internal/httpx"
	"backend/internal/mastery"
	"github.com/gin-gonic/gin"
)

// Mastery is the mastery path use case consumed by the HTTP handler.
//
// It is the reading side of the domain and the two acts a person takes on it
// outside a turn. Building a path is not here, and that absence is the
// design's rather than an omission: a path is created inside a turn, by the
// turn that was asked to work on it (§2.1), so the way to make one is to start
// a turn — the mastery_path capability — not to POST a tree.
type Mastery interface {
	List(context.Context, mastery.ListRequest) ([]mastery.Summary, error)
	Load(context.Context, string) (mastery.View, error)
	Insight(context.Context, string) (mastery.Insight, error)
	UpdateMeta(context.Context, mastery.MetaRequest) (mastery.TopicMeta, error)
	// Holder reports who is writing a path. ErrNotFound when nobody ever has,
	// which is the ordinary state of a path nobody is on rather than a failure.
	Holder(context.Context, string) (mastery.Lease, error)
	// ReleaseAny gives up a path's claim whoever holds it, and reports whether
	// there was one to give up.
	ReleaseAny(context.Context, string) (mastery.Lease, bool, error)
}

// MasteryHandler handles the mastery paths: the list, one path, its insight,
// and the two lease endpoints.
type MasteryHandler struct {
	paths  Mastery
	logger *slog.Logger
}

// NewMasteryHandler constructs the mastery HTTP handler.
func NewMasteryHandler(paths Mastery, logger *slog.Logger) *MasteryHandler {
	return &MasteryHandler{paths: paths, logger: logger}
}

// pathView is one path as a client reads it: what it is for, the tree it holds,
// and the version that guards the tree.
//
// The tree is the domain's own type, serialised as it is stored. It is the one
// place in this package where that is true, and the reason is that the tree's
// JSON *is* a contract — it is the bytes in `state_json`, written and read back
// by the same code — so a second struct with the same fields would be a second
// definition of it, free to drift.
type pathView struct {
	PathID      string       `json:"path_id"`
	Goal        string       `json:"goal"`
	Description string       `json:"description"`
	Emoji       string       `json:"emoji"`
	Status      string       `json:"status"`
	Version     int64        `json:"version"`
	Tree        mastery.Tree `json:"tree"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// summaryView is one row of the path list. It carries the counts and not the
// tree: a client drawing a list of paths needs to know how big each one is, and
// a list that carried every tree would be the whole domain in one response.
type summaryView struct {
	PathID          string    `json:"path_id"`
	Goal            string    `json:"goal"`
	Description     string    `json:"description"`
	Emoji           string    `json:"emoji"`
	Status          string    `json:"status"`
	Version         int64     `json:"version"`
	Modules         int       `json:"modules"`
	KnowledgePoints int       `json:"knowledge_points"`
	Mastered        int       `json:"mastered"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type pathListView struct {
	Paths []summaryView `json:"paths"`
}

// pointView is one knowledge point as the insight reports it: its level, what
// the evidence says about it, and when to come back.
type pointView struct {
	KnowledgePointID string    `json:"knowledge_point_id"`
	Name             string    `json:"name"`
	ModuleID         string    `json:"module_id"`
	Level            string    `json:"level"`
	Total            int       `json:"total"`
	Correct          int       `json:"correct"`
	Incorrect        int       `json:"incorrect"`
	Partial          int       `json:"partial"`
	LastObservedAt   time.Time `json:"last_observed_at"`
	DueAt            time.Time `json:"due_at"`
}

// evidenceView is one observation.
type evidenceView struct {
	EvidenceID       string          `json:"evidence_id"`
	KnowledgePointID string          `json:"knowledge_point_id"`
	Result           string          `json:"result"`
	Source           string          `json:"source,omitempty"`
	AssessmentType   string          `json:"assessment_type,omitempty"`
	Quality          string          `json:"quality,omitempty"`
	Detail           json.RawMessage `json:"detail,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

// stepView is the next move, with the sentence that says why and the evidence
// it stands on.
//
// The two fields COURSE-04 refuses to let be empty are exactly the two the
// domain guarantees non-empty (Step.Validate), so this view can be built from a
// step and cannot be built from anything else.
type stepView struct {
	Kind             string   `json:"kind"`
	KnowledgePointID string   `json:"knowledge_point_id,omitempty"`
	Reason           string   `json:"reason"`
	EvidenceIDs      []string `json:"evidence_ids"`
}

// insightView is one path's answer to "where am I and what next" (MP-05).
type insightView struct {
	Path   pathView    `json:"path"`
	Points []pointView `json:"points"`
	// Weak is the ids of the weak points, most overdue first. It is a view of
	// Points rather than a second list of them, so a client reads one array for
	// the numbers and this one for the ordering.
	Weak     []string       `json:"weak"`
	Evidence []evidenceView `json:"evidence"`
	Next     stepView       `json:"next"`
}

// leaseView is who is writing a path.
type leaseView struct {
	SessionID  string    `json:"session_id"`
	TurnID     string    `json:"turn_id"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// leaseStateView is the answer to both lease endpoints, and it exists because
// "nobody is writing this" is an answer rather than a 404: the path is there and
// simply has no holder. A client therefore reads `lease` as a nullable field and
// does not have to tell an absent path from an unclaimed one by the status code.
type leaseStateView struct {
	PathID string     `json:"path_id"`
	Lease  *leaseView `json:"lease"`
	// Released is what a DELETE reports: whether there was a claim to give up.
	// It is false for a path nobody held, and for a second release of the same
	// one, because in both cases what was asked for is already the state.
	Released bool `json:"released"`
}

// List returns the paths, most recently worked on first.
//
// An empty list is 200 and not 404: a person who has not started studying has
// no paths, which is an answer to "what am I working on" (§2.12).
func (h *MasteryHandler) List(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	request := mastery.ListRequest{SessionID: strings.TrimSpace(c.Query("session_id"))}
	found, err := h.paths.List(c.Request.Context(), request)
	if err != nil {
		h.fail(c, "list_paths_failed", err)
		return
	}
	listed := make([]summaryView, 0, len(found))
	for _, summary := range found {
		listed = append(listed, summaryView{
			PathID: summary.PathID, Goal: summary.Goal, Description: summary.Description,
			Emoji: summary.Emoji, Status: summary.Status, Version: summary.Version,
			Modules: summary.Modules, KnowledgePoints: summary.KnowledgePoints,
			Mastered: summary.Mastered, LastSeenAt: summary.LastSeenAt,
			UpdatedAt: summary.UpdatedAt,
		})
	}
	httpx.WriteSuccess(c, http.StatusOK, pathListView{Paths: listed})
}

// Get returns one path and what it is for.
func (h *MasteryHandler) Get(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	view, err := h.paths.Load(c.Request.Context(), c.Param("pathID"))
	if err != nil {
		h.fail(c, "get_path_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toPathView(view.Path, view.Meta))
}

// metaRequest is one change to what a path is for.
//
// Every field is a pointer so that "leave the goal alone" and "the goal is now
// empty" are different requests, the same shape the question bank's patch uses.
// Which fields may be absent is the domain's rule, not this handler's: it
// refuses a request that names nothing, and refuses a status this version does
// not write.
type metaRequest struct {
	Goal        *string `json:"goal"`
	Description *string `json:"description"`
	Emoji       *string `json:"emoji"`
	Status      *string `json:"status"`
}

// Patch rewrites what a path is for.
//
// It changes no tree and takes no version, which is why it can be a PATCH on a
// path whose turn is running: it is the one write in this domain that does not
// touch the state the version guard protects. The answer is the path as it now
// stands, so that a client does not have to follow a write with a read.
func (h *MasteryHandler) Patch(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var request metaRequest
	if !readJSON(c, maxSessionRequestBytes, &request) {
		return
	}
	if request.Goal == nil && request.Description == nil && request.Emoji == nil && request.Status == nil {
		httpx.WriteError(c, http.StatusBadRequest, "empty_change", "the request names nothing to change")
		return
	}
	pathID := c.Param("pathID")
	_, err := h.paths.UpdateMeta(c.Request.Context(), mastery.MetaRequest{
		PathID: pathID, Goal: request.Goal, Description: request.Description,
		Emoji: request.Emoji, Status: request.Status,
	})
	if err != nil {
		h.fail(c, "update_path_failed", err)
		return
	}
	view, err := h.paths.Load(c.Request.Context(), pathID)
	if err != nil {
		h.fail(c, "update_path_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toPathView(view.Path, view.Meta))
}

// Insight returns the path's state: the goal, the evidence, the weak points,
// the review state and the next move with its reason (MP-05).
func (h *MasteryHandler) Insight(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	insight, err := h.paths.Insight(c.Request.Context(), c.Param("pathID"))
	if err != nil {
		h.fail(c, "read_insight_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toInsightView(insight))
}

// Lease returns who is writing a path.
func (h *MasteryHandler) Lease(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	pathID := c.Param("pathID")
	lease, err := h.paths.Holder(c.Request.Context(), pathID)
	if err != nil && !errors.Is(err, mastery.ErrNotFound) {
		h.fail(c, "read_lease_failed", err)
		return
	}
	if errors.Is(err, mastery.ErrNotFound) {
		// Nobody has ever claimed it. The path itself is still checked, so that
		// a lease read of a path that does not exist is a 404 rather than a
		// confident "nobody is writing this".
		if _, err := h.paths.Load(c.Request.Context(), pathID); err != nil {
			h.fail(c, "read_lease_failed", err)
			return
		}
		httpx.WriteSuccess(c, http.StatusOK, leaseStateView{PathID: pathID})
		return
	}
	held := toLeaseView(lease)
	httpx.WriteSuccess(c, http.StatusOK, leaseStateView{PathID: pathID, Lease: &held})
}

// ReleaseLease gives up a path's claim, whoever holds it (the surface's
// `mastery_leave`).
//
// It is not the holder's own act and it cannot be: a turn whose lease was taken
// over while it was still running would be releasing somebody else's claim by
// asking. So the endpoint takes the decision on behalf of the person at the
// keyboard — "nobody is writing this any more" — and reports whether there was
// a claim to give up. Releasing a path nobody holds is 200 with
// `released: false`, not a 404: the path is there, and the state that was asked
// for is the state it is already in.
func (h *MasteryHandler) ReleaseLease(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	pathID := c.Param("pathID")
	lease, released, err := h.paths.ReleaseAny(c.Request.Context(), pathID)
	if err != nil {
		h.fail(c, "release_lease_failed", err)
		return
	}
	state := leaseStateView{PathID: pathID, Released: released}
	if released {
		gone := toLeaseView(lease)
		state.Lease = &gone
	}
	httpx.WriteSuccess(c, http.StatusOK, state)
}

func toPathView(path mastery.Path, meta mastery.TopicMeta) pathView {
	return pathView{
		PathID:      path.ID,
		Goal:        meta.Goal,
		Description: meta.Description,
		Emoji:       meta.Emoji,
		Status:      meta.Status,
		Version:     path.Version,
		Tree:        path.Tree,
		CreatedAt:   path.CreatedAt,
		UpdatedAt:   path.UpdatedAt,
	}
}

func toLeaseView(lease mastery.Lease) leaseView {
	return leaseView{
		SessionID:  lease.SessionID,
		TurnID:     lease.TurnID,
		AcquiredAt: lease.AcquiredAt,
	}
}

func toInsightView(insight mastery.Insight) insightView {
	points := make([]pointView, 0, len(insight.Points))
	for _, point := range insight.Points {
		points = append(points, pointView{
			KnowledgePointID: point.ID, Name: point.Name, ModuleID: point.ModuleID,
			Level: string(point.Level),
			Total: point.Total, Correct: point.Correct, Incorrect: point.Incorrect,
			Partial: point.Partial, LastObservedAt: point.LastObservedAt, DueAt: point.DueAt,
		})
	}
	evidences := make([]evidenceView, 0, len(insight.Evidence))
	for _, evidence := range insight.Evidence {
		evidences = append(evidences, evidenceView{
			EvidenceID: evidence.ID, KnowledgePointID: evidence.KnowledgePoint,
			Result: string(evidence.Result), Source: evidence.Source,
			AssessmentType: evidence.AssessmentType, Quality: evidence.Quality,
			Detail: evidence.Detail, CreatedAt: evidence.CreatedAt,
		})
	}
	weak := make([]string, 0, len(insight.Weak))
	weak = append(weak, insight.Weak...)
	return insightView{
		Path:   toPathView(insight.Path, insight.Meta),
		Points: points,
		Weak:   weak,
		// The list is empty rather than null for a path nothing has been
		// observed on, which is the same "nothing is an answer" rule the list
		// endpoint follows.
		Evidence: evidences,
		Next: stepView{
			Kind:             string(insight.Next.Kind),
			KnowledgePointID: insight.Next.KnowledgePointID,
			Reason:           insight.Next.Reason,
			EvidenceIDs:      insight.Next.EvidenceIDs,
		},
	}
}

func (h *MasteryHandler) ready(c *gin.Context) bool {
	if h != nil && h.paths != nil {
		return true
	}
	httpx.WriteError(c, http.StatusInternalServerError, "mastery_unavailable", "the mastery paths are not configured")
	return false
}

// fail reports a mastery failure, distinguishing what the client can act on
// from what only an operator can.
//
// A path that is not there is the one 404, and it covers a knowledge point as
// well: a knowledge point is a coordinate inside one path's tree, so one that
// does not resolve is a path whose tree does not hold it, which is the same
// answer from the outside.
func (h *MasteryHandler) fail(c *gin.Context, code string, err error) {
	switch {
	case errors.Is(err, mastery.ErrNotFound):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such path")
	case errors.Is(err, mastery.ErrInvalidRequest):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request",
			detail(err, mastery.ErrInvalidRequest))
	// The three ways a write can lose to another writer. None of them is
	// reachable from this handler's own writes — nothing here takes a version or
	// a lease — and they are mapped anyway, because a 500 for "somebody else got
	// there first" would be this endpoint blaming itself for a race it is
	// reporting correctly.
	case errors.Is(err, mastery.ErrStaleState):
		httpx.WriteError(c, http.StatusConflict, "stale_path", detail(err, mastery.ErrStaleState))
	case errors.Is(err, mastery.ErrDuplicate):
		httpx.WriteError(c, http.StatusConflict, "conflict", detail(err, mastery.ErrDuplicate))
	case errors.Is(err, mastery.ErrLeaseHeld):
		httpx.WriteError(c, http.StatusConflict, "path_in_use", detail(err, mastery.ErrLeaseHeld))
	default:
		if h.logger != nil {
			h.logger.Error("mastery request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, code, "the mastery request failed")
	}
}
