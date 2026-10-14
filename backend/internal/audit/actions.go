package audit

// The closed set of actions (§2.6).
//
// It is a list in code rather than a set of strings at the call sites for the
// reason the 15 turn-event types are a table: an action nobody can spell the
// same way twice is an action nobody can read back, and the failure is the
// quiet kind — the row is written, and the query that should have found it
// returns nothing, which reads as "this never happened".
//
// The names are `<area>.<verb>` in the past tense, because each one is a
// sentence about something that was done and not a permission to do it. The
// areas follow the packages this product is made of, so a new area's first
// action is added where the area is.
//
// There is no `authorization.denied`. A refusal is recorded under the action
// that was attempted, with ResultDenied — which is what makes "what has this
// account tried and been refused" one query rather than two.
//
// `surface.denied` is the one action that is not a change of state, and it is
// here because of where it is written: the middleware that refuses a request
// before its handler runs (§2.1) knows the area of the product that was reached
// for and nothing more, so a surface is the whole of the attempt it can name.
// Everything it can say about *what* was attempted is therefore the one field a
// surface has — its name — and the attempted action is left to the handlers
// that do know it, which record their own refusals under their own actions.

// Action is one thing this build records.
type Action string

const (
	// ActionSurfaceDenied is a request refused before its handler ran, because
	// the caller does not reach the area of the product the route belongs to
	// (§2.1). The surface is its target and the layer that said no is in its
	// detail.
	ActionSurfaceDenied Action = "surface.denied"
	// ActionAccountCreated is a registration.
	ActionAccountCreated Action = "account.created"
	// ActionPresetChanged is an administrator moving an account between
	// presets.
	ActionPresetChanged Action = "account.preset_changed"
	// ActionGrantWritten is an administrator turning one item on or off.
	ActionGrantWritten Action = "grant.written"
	// ActionGrantWithdrawn is an administrator removing the decision recorded
	// for one item, which returns it to the account's baseline.
	ActionGrantWithdrawn Action = "grant.withdrawn"
	// ActionGuardianLinked is a relationship being created.
	ActionGuardianLinked Action = "guardian.linked"
	// ActionGuardianRevoked is a relationship being removed.
	ActionGuardianRevoked Action = "guardian.revoked"
	// ActionGuardianPermissionGranted is one permission added to a relationship.
	ActionGuardianPermissionGranted Action = "guardian.permission_granted"
	// ActionGuardianPermissionRevoked is one permission taken back.
	ActionGuardianPermissionRevoked Action = "guardian.permission_revoked"
	// ActionReportsViewed is a guardian reading a learner's reports (AC-11).
	// It is recorded on success as well as on refusal, because reaching into
	// another account's material is the thing this table exists to be able to
	// answer, not only the attempts that were refused.
	ActionReportsViewed Action = "guardian.reports_viewed"
	// ActionSessionClaimed is an account taking an ownerless conversation.
	ActionSessionClaimed Action = "session.claimed"
	// ActionSessionDeleted is a conversation removed for good.
	ActionSessionDeleted Action = "session.deleted"
	// ActionFileDeleted is a file taken out of the library and put in the recycle
	// bin. The bytes stay where they are; what changed is that the file is no
	// longer in the library (§2.14).
	ActionFileDeleted Action = "library.file_deleted"
	// ActionFileRestored is a file coming back out of the recycle bin, whether it
	// was asked for by name or by uploading the same bytes again — one action
	// because it is one change of state, and the detail says which way it was
	// asked for.
	ActionFileRestored Action = "library.file_restored"
	// ActionFilePurged is a file destroyed: the bytes, and the row. It is recorded
	// when it is refused as well as when it is done, because "somebody tried to
	// destroy this and was told no" is what AC-15 is asked about afterwards.
	ActionFilePurged Action = "library.file_purged"
	// ActionWorkspaceExported is a workspace packed out.
	ActionWorkspaceExported Action = "workspace.exported"
	// ActionWorkspaceImported is a workspace packed in.
	ActionWorkspaceImported Action = "workspace.imported"
)

// Actions is every action, in the order the areas are listed above. It is the
// list the check is written from, so that adding one is adding a line here.
var Actions = []Action{
	ActionSurfaceDenied,
	ActionAccountCreated, ActionPresetChanged,
	ActionGrantWritten, ActionGrantWithdrawn,
	ActionGuardianLinked, ActionGuardianRevoked,
	ActionGuardianPermissionGranted, ActionGuardianPermissionRevoked,
	ActionReportsViewed,
	ActionSessionClaimed, ActionSessionDeleted,
	ActionFileDeleted, ActionFileRestored, ActionFilePurged,
	ActionWorkspaceExported, ActionWorkspaceImported,
}

// KnownAction reports whether a name is one this build records.
func KnownAction(action Action) bool { return contains(Actions, action) }
