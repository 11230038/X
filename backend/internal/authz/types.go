// Package authz answers what an account may do, and whose rows it may see.
//
// It exists because V5 introduced the first request in this repository that
// reads somebody else's data on purpose. Until then every protected read was
// "mine": the owner came from the token and the query filtered on it, which is a
// shape that needs no vocabulary. A guardian reading a learner's reports is a
// different shape — the reader and the subject are two accounts — and it only
// works if "why is this allowed" is something that can be stored, revoked and
// pointed at afterwards.
//
// The package is deliberately thin at the bottom. Caller is what every storage
// seam in this repository takes to narrow a read to one account, and it is here
// rather than in each domain so that "an account is asking" is one type rather
// than six.
package authz

import (
	"errors"
	"time"
)

// The two roles. PRD §5.1 and §23.1 第 7 条 are explicit that this is the whole
// list: standard, learner and custom are presets carried by an ordinary account,
// not a third kind of account. A build that invented a "learner role" would be
// answering "may this person use the tool" with the same field that answers "is
// this person the administrator", and the first question is per-item.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// Roles is every role this build writes.
//
// It is a list rather than a pair of constants for the same reason Presets is:
// the account service has to refuse a third role, and the refusal belongs to
// this package's vocabulary rather than to a comparison written where the role
// is stored. A second `role == "root"`-style check elsewhere is exactly the
// second private decision §7.1 refuses.
var Roles = []string{RoleAdmin, RoleUser}

// KnownRole reports whether a name is a role this build writes.
func KnownRole(role string) bool { return contains(Roles, role) }

// Caller is the account a request is being served for.
//
// Role travels with the id rather than being looked up where it is needed,
// because RequireAuth already reloads the account on every request precisely so
// that disabling it takes effect immediately; passing that answer along costs
// nothing and asking the database a second time would let the two disagree.
type Caller struct {
	UserID int64
	Role   string
}

// Valid reports whether this caller names a real account. A zero id means the
// route was reached without one, which is a wiring fault rather than a denial.
func (c Caller) Valid() bool { return c.UserID > 0 }

// IsAdmin reports whether this caller is the instance's administrator.
//
// This is the only place in the repository that compares a role. Everything else
// asks a question about a permission, so that when the four layers of V5's
// authorization model are combined they are combined in one place — a handler
// that wrote `user.Role == "admin"` would be making the second, private decision
// that §2.1 exists to prevent.
//
// An administrator sees rows that belong to nobody. That is not a privilege so
// much as a handover: every row written before V5 has a NULL owner, and somebody
// has to be able to see them, claim them and give them away.
func (c Caller) IsAdmin() bool { return c.Role == RoleAdmin }

// ── the vocabulary ──────────────────────────────────────────────────────────
//
// The three closed sets below are the shape of what may be written down, not
// decisions about it. Two of them — the kinds and the permissions — are the
// CHECK constraints of migration 00016 restated where Go can read them, and
// the third is the presets users.preset may carry. They live here, with
// Caller, because they are the words the rest of the package is written in.
// Kind names what a decision is about. The set is closed by migration 00016's
// CHECK on user_grants.item_kind: a kind that is not one of these cannot be
// written down, so it cannot be decided about either.
type Kind string

const (
	// KindModel is a model or a provider.
	KindModel Kind = "model"
	// KindCapability is one of the capabilities this build registers.
	KindCapability Kind = "capability"
	// KindSurface is a user-visible area of the product.
	KindSurface Kind = "surface"
	// KindKB is a knowledge base.
	KindKB Kind = "kb"
	// KindTool is a tool a capability may call.
	KindTool Kind = "tool"
	// KindExec is running something on the host.
	KindExec Kind = "exec"
	// KindLearningPolicy is the learner's policy as a whole.
	KindLearningPolicy Kind = "learning_policy"
)

// Kinds is every kind user_grants may name, in the order the CHECK lists.
var Kinds = []Kind{
	KindModel, KindCapability, KindSurface, KindKB,
	KindTool, KindExec, KindLearningPolicy,
}

// KnownKind reports whether a kind is one this build decides about.
func KnownKind(kind Kind) bool {
	for _, candidate := range Kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

// Surface names one user-visible area of the product — the places a screen can
// be, as opposed to the capabilities that fill them.
//
// The set is closed here for the same reason the kinds are closed by the CHECK,
// and §5 is explicit about what depends on it: AUTHZ_LEARNER_SURFACES must name
// surfaces "这个 build 注册过的", so a deployment that wrote `AUTHZ_LEARNER_
// SURFACES=calender` (or any other word that is not a route in router.go) has to
// be refused at startup rather than silently narrow a learner to nothing. That
// check can only exist if there is one list to check against, and this is it.
//
// The names are the route groups in internal/server/router.go, not the package
// names, because a surface is what a person reaches: `notebook` is where the
// question bank, the categories and the practice queue appear.
const (
	// SurfaceChat is talking to the assistant: the sessions and the socket.
	SurfaceChat = "chat"
	// SurfaceLibrary is the file library.
	SurfaceLibrary = "library"
	// SurfaceKnowledgeBase is the collections and what is searched in them.
	SurfaceKnowledgeBase = "knowledge_base"
	// SurfaceNotebook is the question bank and its categories.
	SurfaceNotebook = "notebook"
	// SurfacePractice is the review queue and the statistics over it.
	SurfacePractice = "practice"
	// SurfaceMastery is the paths and their insights.
	SurfaceMastery = "mastery"
	// SurfaceReading is the reader: materials, marks and the aids over them.
	SurfaceReading = "reading"
	// SurfaceBook is generated books: the shelf, the outline and the chapters.
	// It is separate from reading because a book is written by a model from a
	// source the reader chose, and "may read" is not "may spend model calls on
	// books".
	SurfaceBook = "book"
	// SurfaceWatching is video learning: the keyword search and the player.
	// What it reaches is another site's public search, which is why it is a
	// surface of its own rather than part of reading.
	SurfaceWatching = "watching"
	// SurfaceDashboard is the one read that spans the domains.
	SurfaceDashboard = "dashboard"
	// SurfaceMemory is what the system has been told and has admitted.
	SurfaceMemory = "memory"
	// SurfaceSystem is what a build reports about itself: the capability list
	// and the account's own profile.
	SurfaceSystem = "system"
	// SurfaceAdmin is the governance routes: grants, relationships, audits.
	SurfaceAdmin = "admin"
	// SurfaceGuardian is the other side of a relationship: what an account
	// holds over somebody it watches. It is separate from admin because the two
	// are different accounts — an administrator governs the instance, a guardian
	// has been given one learner — and a single surface would make "may this
	// account be here at all" answer both.
	SurfaceGuardian = "guardian"
)

// Surfaces is every area this build has a route for. It is the list §5's
// startup check reads, so a surface that is added here and mounted on nothing
// is a name a configuration may use and a request can never reach.
var Surfaces = []string{
	SurfaceChat, SurfaceLibrary, SurfaceKnowledgeBase, SurfaceNotebook,
	SurfacePractice, SurfaceMastery, SurfaceReading, SurfaceBook, SurfaceWatching,
	SurfaceDashboard, SurfaceMemory, SurfaceSystem, SurfaceAdmin, SurfaceGuardian,
}

// KnownSurface reports whether a name is a surface registered by this build.
func KnownSurface(id string) bool { return contains(Surfaces, id) }

// The presets users.preset may carry. standard, learner and custom are what an
// ordinary account starts from; they are not roles (§2.1), and PRD §23.1 第 7
// 条 asked about the naming precisely so that they would not be read as one.
const (
	PresetStandard = "standard"
	PresetLearner  = "learner"
	PresetCustom   = "custom"
)

// NormalizePreset reads the stored preset. The empty string is what every
// account registered before V5 carries, and it is read as standard — not out of
// leniency but out of accuracy: those accounts can today do everything a
// standard account can, and reading them as restricted would be a tightening
// nobody asked for (§2.2).
func NormalizePreset(preset string) string {
	switch preset {
	case PresetLearner, PresetCustom:
		return preset
	default:
		return PresetStandard
	}
}

// Presets is every preset this build writes down, in the order the model lists
// them. NormalizePreset answers a *read* — anything it does not recognise is
// standard, because that is what such an account can in fact do — so it cannot
// be used to validate a write, where an unknown name is a caller inventing
// vocabulary rather than a historical row to be read charitably.
var Presets = []string{PresetStandard, PresetLearner, PresetCustom}

// KnownPreset reports whether a name is one this build writes to users.preset.
func KnownPreset(preset string) bool { return contains(Presets, preset) }

// Permission is one of the four things a guardian link may allow (AUTH-05).
// The row's existence is the permission: there is no boolean to be read the
// wrong way round, and "may this guardian do this" is a question about whether
// a row is there (§2.3).
type Permission string

const (
	// PermViewReports lets a guardian see the learner's reports.
	PermViewReports Permission = "view_reports"
	// PermAssignMaterials lets a guardian put material in front of the learner.
	PermAssignMaterials Permission = "assign_materials"
	// PermManageRestrictions lets a guardian narrow the learner's policy.
	PermManageRestrictions Permission = "manage_restrictions"
	// PermResetCredentials lets a guardian reset the learner's credentials.
	PermResetCredentials Permission = "reset_credentials"
)

// Permissions is every permission, in the order migration 00016's CHECK lists.
var Permissions = []Permission{
	PermViewReports, PermAssignMaterials, PermManageRestrictions, PermResetCredentials,
}

// KnownPermission reports whether a permission is one this build recognises.
func KnownPermission(permission Permission) bool {
	for _, candidate := range Permissions {
		if candidate == permission {
			return true
		}
	}
	return false
}

// Item is one thing a decision is about: a kind and, optionally, one item of
// it. The empty ID is the whole kind — the distinction user_grants.item_id
// draws with the empty string rather than NULL, because NULL repeats in a
// unique index and "the decision about this kind" would then be several rows
// that all claim to be it (§3, 00016).
type Item struct {
	Kind Kind
	ID   string
}

// Whole returns the item that stands for every item of a kind.
func Whole(kind Kind) Item { return Item{Kind: kind} }

// Grant is one row of user_grants as the policy reads it: which item, and
// whether it is being turned on or off. A grant that turns something off is
// what makes "the baseline plus grants" a description of a baseline rather
// than a competing one (§2.2).
type Grant struct {
	Item    Item
	Allowed bool
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// union joins two lists, keeping each value once and keeping the order the
// caller gave. The order matters because Items() is what an administrator is
// shown, and a floor that shuffled itself between requests would be one they
// could not compare against a previous screen.
func union(base, added []string) []string {
	joined := make([]string, 0, len(base)+len(added))
	for _, source := range [][]string{base, added} {
		for _, value := range source {
			if value != "" && !contains(joined, value) {
				joined = append(joined, value)
			}
		}
	}
	return joined
}

// ── what the store keeps ────────────────────────────────────────────────────

// Link is one guardian relationship, with the permissions it carries.
//
// The permissions travel with the link rather than being asked for separately
// because they are always needed together: a decision about somebody else's
// material is "is there a link" and "does it carry this", and two reads would
// let the two answers come from two different moments.
type Link struct {
	ID          string
	GuardianID  int64
	LearnerID   int64
	CreatedBy   int64
	Permissions []Permission
	CreatedAt   time.Time
}

// Holds reports whether the link carries a permission.
func (l Link) Holds(permission Permission) bool {
	for _, held := range l.Permissions {
		if held == permission {
			return true
		}
	}
	return false
}

// Assignment is one row of user_grants as stored: which item, whether it is
// turned on or off, and who wrote it down.
type Assignment struct {
	GrantID   int64
	UserID    int64
	Item      Item
	Allowed   bool
	GrantedBy int64
	CreatedAt time.Time
}

// Grant is the assignment as the policy reads it.
func (a Assignment) Grant() Grant { return Grant{Item: a.Item, Allowed: a.Allowed} }

// Grants converts a set of assignments to the policy's form. It exists so that
// only one place does the conversion, and that place is next to the two types.
func Grants(assignments []Assignment) []Grant {
	grants := make([]Grant, 0, len(assignments))
	for _, assignment := range assignments {
		grants = append(grants, assignment.Grant())
	}
	return grants
}

// Account is one row of the governance list: which account, and the two
// identity layers it carries.
//
// Username and Disabled are the account's own columns rather than this
// package's, and they travel here because the list is drawn a row at a time: an
// administrator choosing whom to set a preset for is reading a name, and a list
// of ids is a list nothing can be decided from. The password hash is not here
// and never will be (§12).
type Account struct {
	UserID   int64
	Username string
	Role     string
	Preset   string
	Disabled bool
}

// PresetOf is the preset as the policy reads it, which is not the stored string
// for an account that predates V5.
func (a Account) PresetOf() string { return NormalizePreset(a.Preset) }

// The refusals the store and the service answer with. They are sentinels
// because each of them is a different sentence to a caller — a link that
// already exists is not the same mistake as an account that is not a learner —
// and a caller reading strings would be matching on prose.
var (
	// ErrSelfLink reports an account named as its own guardian. Migration
	// 00016's CHECK refuses it as well; this is the sentence rather than the
	// constraint violation.
	ErrSelfLink = errors.New("authz: an account may not be its own guardian")
	// ErrNotALearner reports a relationship whose watched account is not a
	// learner. It is a cross-table question — it reads users.preset — so a
	// CHECK cannot ask it and this is the only place it is asked.
	ErrNotALearner = errors.New("authz: the watched account is not a learner")
	// ErrGuardianIsLearner reports a learner named as somebody's guardian,
	// which is the third of AUTH-04's prohibitions.
	ErrGuardianIsLearner = errors.New("authz: a learner may not be a guardian")
	// ErrLinkExists reports a relationship already in the table. It is the
	// unique key's 1062 mapped to something callers can branch on.
	ErrLinkExists = errors.New("authz: the relationship already exists")
	// ErrLinkNotFound reports a relationship that is not there — including one
	// that was revoked, because revoking deletes the row (§2.3).
	ErrLinkNotFound = errors.New("authz: no such relationship")
	// ErrPermissionNotHeld reports a permission the link does not carry, on the
	// path that removes one.
	ErrPermissionNotHeld = errors.New("authz: the link does not carry that permission")
	// ErrGrantNotFound reports a grant that is not there.
	ErrGrantNotFound = errors.New("authz: no such grant")
	// ErrUnknownPermission reports a permission outside AUTH-05's four.
	ErrUnknownPermission = errors.New("authz: not a permission this build recognises")
	// ErrUnknownUser reports an account id the users table does not have.
	ErrUnknownUser = errors.New("authz: no such account")
	// ErrUnknownPreset reports a preset being written that this build does not
	// define. It is a write-path refusal only: a preset read back that this
	// build does not recognise is read as standard (NormalizePreset), because
	// that is what such an account can in fact do, but a name being written has
	// to be one of the three or nothing stops a typo from becoming a preset
	// nobody defined.
	ErrUnknownPreset = errors.New("authz: not a preset this build writes")
)
