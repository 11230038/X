// Package session owns conversations: the sessions they live in and the
// messages they are made of.
//
// A session is the unit a client lists, renames and deletes, and the unit a turn
// belongs to. It owns the two tables that outlive a turn — `sessions` and
// `messages` — while the record of what happened inside a turn belongs to the
// turn runtime.
package session

import (
	"context"
	"errors"
	"time"

	"backend/internal/authz"
)

var (
	// ErrNotFound reports a session that does not exist, or does not exist in
	// the state the operation acts on. The recycle bin makes those two cases
	// one: a live session is not in the bin, so restoring it is not found.
	ErrNotFound = errors.New("session: not found")
	// ErrInvalidTitle reports a title that would not be readable back.
	ErrInvalidTitle = errors.New("session: invalid title")
	// ErrInvalidMessage reports a message that could not be appended.
	ErrInvalidMessage = errors.New("session: invalid message")
	// ErrInvalidKind reports a session kind this version does not know. It is a
	// programming error at a call site rather than something a client can
	// correct in the request — the HTTP boundary parses and rejects the wire
	// value before it gets here.
	ErrInvalidKind = errors.New("session: invalid kind")
)

// Kind says which surface a conversation belongs to: the chat page, the
// writing page, the mastery path's conversation page or the question bank's
// conversation page. It is a partition of one table and not a permission: every
// kind still runs a capability behind the chat surface, as its page requires.
//
// The zero value is chat on purpose. Every caller that existed before the
// other pages were a thing expected chat, and a listing that defaulted to
// "all" would mix the lists the first time somebody forgot to pass a kind.
type Kind string

// The kinds this version writes.
const (
	KindChat         Kind = "chat"
	KindWriting      Kind = "writing"
	KindMastery      Kind = "mastery"
	KindQuestionBank Kind = "question_bank"
)

// Valid reports whether the kind is one this version writes.
func (k Kind) Valid() bool {
	return k == KindChat || k == KindWriting || k == KindMastery || k == KindQuestionBank
}

// normalizeKind maps the empty value to chat, which is what a zero-value
// request or an old stored row means.
func normalizeKind(kind Kind) Kind {
	if kind == "" {
		return KindChat
	}
	return kind
}

// TitleMaxRunes bounds a stored title. The column is TEXT and would accept far
// more, but a title exists to be read in a list, and a limit that is enforced
// where it is written is cheaper than one the client discovers in rendering.
const TitleMaxRunes = 200

// Role is who wrote a message.
//
// The column holds any short string, but only the roles this version writes are
// named here. Tool traffic is not one of them: what a turn's tools returned is
// part of that turn's context and is recorded as its events, not as things said
// in the conversation.
type Role string

// The roles a stored message may have.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Valid reports whether the role is one a message may be stored with.
func (r Role) Valid() bool {
	return r == RoleUser || r == RoleAssistant
}

// Session is a conversation.
type Session struct {
	ID    string
	Title string
	// Kind is which list the session belongs to. A session read from storage
	// always has one; a session being created may leave it empty, which means
	// chat.
	Kind Kind
	// OwnerID is the account the conversation belongs to, or zero for a row
	// written before V5 (migration 00014 left every earlier row NULL). Zero is a
	// state rather than a missing value: such a session belongs to nobody, is
	// visible to the administrator and to nobody else, and is claimed explicitly.
	OwnerID int64
	// CreatedAt and UpdatedAt are when the session was made and when anything
	// last happened in it. The list is ordered by the latter, so appending a
	// message moves a session to the top.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt is when the session entered the recycle bin, or the zero time
	// while it is live. It is the only field that says which state the session
	// is in: the two columns in storage are kept in step by a check constraint,
	// so carrying both here would be two answers to one question.
	DeletedAt time.Time
}

// Deleted reports whether the session is in the recycle bin.
func (s Session) Deleted() bool { return !s.DeletedAt.IsZero() }

// Message is one thing said in a session, by the user or by the assistant.
type Message struct {
	ID        string
	SessionID string
	Role      Role
	Content   string
	// Capability names what produced an assistant message. It is empty on the
	// user's own messages, which no capability produced.
	Capability string
	CreatedAt  time.Time
}

// NewMessage is a message about to be appended.
type NewMessage struct {
	SessionID  string
	Role       Role
	Content    string
	Capability string
}

// ListOptions select which sessions a listing returns.
type ListOptions struct {
	// Caller narrows the listing to one account. A list with no caller is not a
	// list of everything: before V5 these routes answered every session in the
	// database, which was right only because the database held one person's work.
	Caller authz.Caller
	// Deleted lists the recycle bin instead of the live sessions. The two are
	// never listed together: a client showing both would have to filter every
	// row to know which actions it may offer.
	Deleted bool
	// Kind narrows the listing to one surface. Its zero value is chat, matching
	// every caller that existed before the other surfaces did: a listing that
	// defaulted to everything would put writing, mastery or question-bank
	// history in the chat list the first time a caller forgot the field.
	Kind Kind
	// AllKinds asks for every surface. It is explicit because showing
	// everything is a decision (the recycle bin, a workspace export), not a
	// default.
	AllKinds bool
	// Limit and Offset page the listing. A zero Limit takes the default; a
	// Limit above the maximum is clamped rather than rejected, since an
	// oversized page is a client asking for more than it should get, not an
	// error it could correct.
	Limit  int
	Offset int
}

// Repository is the storage seam. It reports what storage holds and lets the
// service decide what that means: whether a session is live or in the bin, and
// which operations each state allows, are rules that belong above it.
//
// Every read here takes a caller and narrows itself to that account's rows.
// Ownership is a filter rather than a check: a session belonging to somebody
// else is not refused, it is absent, which is why a request for it is answered
// exactly as a request for an id that was never issued (AUTH-06).
type Repository interface {
	Create(ctx context.Context, record Session) error
	// Session returns the session whatever state it is in, so the service can
	// apply that state's rules itself. "Whatever state" is about the recycle bin
	// and not about ownership: a row another account owns is not returned at all.
	Session(ctx context.Context, sessionID string, caller authz.Caller) (Session, error)
	List(ctx context.Context, options ListOptions) ([]Session, error)
	// Rename and SetDeleted report ErrNotFound when the session is not there,
	// so a caller cannot believe it changed a row that does not exist.
	Rename(ctx context.Context, sessionID, title string, now time.Time, caller authz.Caller) error
	SetDeleted(ctx context.Context, sessionID string, deleted bool, now time.Time, caller authz.Caller) error
	Delete(ctx context.Context, sessionID string, caller authz.Caller) error
	// Claim gives a session that belongs to no account to the caller. It is the
	// exit from the NULL state §2.5 defines: every row written before V5 belongs
	// to nobody, only the administrator can see one, and without this there would
	// be no way to hand one out short of writing SQL.
	//
	// Only a session with no owner can be claimed. A session that already has one
	// is not something a second account takes over — that would be the
	// cross-account write this whole version exists to stop — and the caller
	// cannot see one anyway. Whether the claim happened is answered by the state
	// afterwards rather than by an error: claiming a session that is already the
	// caller's is the same outcome, so it succeeds and does nothing.
	Claim(ctx context.Context, sessionID string, caller authz.Caller) error
	// AppendMessage stores a message and marks the session as having activity,
	// in one transaction: a message nobody can find the session for would be
	// invisible, and a session marked active with nothing added would be a lie.
	AppendMessage(ctx context.Context, record Message, caller authz.Caller) error
	Messages(ctx context.Context, sessionID string, caller authz.Caller) ([]Message, error)
}

// List defaults. A listing with no bound is a listing that grows without one.
const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)
