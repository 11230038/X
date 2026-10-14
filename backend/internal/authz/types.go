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

// The two roles. PRD §5.1 and §23.1 第 7 条 are explicit that this is the whole
// list: standard, learner and custom are presets carried by an ordinary account,
// not a third kind of account. A build that invented a "learner role" would be
// answering "may this person use the tool" with the same field that answers "is
// this person the administrator", and the first question is per-item.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

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
