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
)

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
	// Deleted lists the recycle bin instead of the live sessions. The two are
	// never listed together: a client showing both would have to filter every
	// row to know which actions it may offer.
	Deleted bool
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
type Repository interface {
	Create(ctx context.Context, record Session) error
	// Session returns the session whatever state it is in, so the service can
	// apply that state's rules itself.
	Session(ctx context.Context, sessionID string) (Session, error)
	List(ctx context.Context, options ListOptions) ([]Session, error)
	// Rename and SetDeleted report ErrNotFound when the session is not there,
	// so a caller cannot believe it changed a row that does not exist.
	Rename(ctx context.Context, sessionID, title string, now time.Time) error
	SetDeleted(ctx context.Context, sessionID string, deleted bool, now time.Time) error
	Delete(ctx context.Context, sessionID string) error
	// AppendMessage stores a message and marks the session as having activity,
	// in one transaction: a message nobody can find the session for would be
	// invisible, and a session marked active with nothing added would be a lie.
	AppendMessage(ctx context.Context, record Message) error
	Messages(ctx context.Context, sessionID string) ([]Message, error)
}

// List defaults. A listing with no bound is a listing that grows without one.
const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)
