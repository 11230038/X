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
	// ActionAccountProvisioned is a super administrator creating an account.
	// It is separate from ActionAccountCreated because the two are different
	// sentences about different actors: one is somebody joining, and the other
	// is an administrator deciding somebody exists.
	ActionAccountProvisioned Action = "account.provisioned"
	// ActionAccountDeleted is a super administrator removing an account and
	// everything the schema cascades from it. The entry survives the account,
	// because the audit carries no foreign key.
	ActionAccountDeleted Action = "account.deleted"
	// ActionPasswordReset is a super administrator replacing another account's
	// password without knowing the old one. The account's own password change
	// is deliberately not here: it is a thing an account does to itself.
	ActionPasswordReset Action = "account.password_reset"
	// ActionAccountDisabled and ActionAccountEnabled are the two directions of
	// the sign-in flag. They are two actions rather than one carrying a boolean
	// because each is a sentence an auditor asks for by name — "who shut this
	// account off" and "who let it back in" are different questions.
	ActionAccountDisabled Action = "account.disabled"
	ActionAccountEnabled  Action = "account.enabled"
	// ActionPresetChanged is an administrator moving an account between
	// presets.
	ActionPresetChanged Action = "account.preset_changed"
	// ActionSessionsRevoked is an administrator taking back every token one
	// account holds (§2.4). The account's own sign-out is deliberately not here:
	// it is a thing an account does to itself, and this table is about who did
	// what to somebody else. What makes this worth a row is that the sessions an
	// administrator ends are not the administrator's own.
	ActionSessionsRevoked Action = "account.sessions_revoked"
	// ActionUnlocked is an administrator clearing the run of failed sign-ins that
	// locked an account, which lets its owner in before the lock would have run
	// out on its own (§2.3).
	ActionUnlocked Action = "account.unlocked"
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
	// ActionWorkspaceRecovered is an import that did not commit having its rows
	// taken back out (§2.13). It is its own action rather than an import recorded
	// with a reason, because it is the one action in this list that *removes*
	// somebody's rows: an auditor reading "imported, ok" beside a failed import
	// would be reading the opposite of what happened.
	ActionWorkspaceRecovered Action = "workspace.recovered"
)

// Actions is every action, in the order the areas are listed above. It is the
// list the check is written from, so that adding one is adding a line here.
var Actions = []Action{
	ActionSurfaceDenied,
	ActionAccountCreated, ActionAccountProvisioned, ActionAccountDeleted,
	ActionPasswordReset, ActionAccountDisabled, ActionAccountEnabled,
	ActionPresetChanged,
	ActionSessionsRevoked, ActionUnlocked,
	ActionGrantWritten, ActionGrantWithdrawn,
	ActionGuardianLinked, ActionGuardianRevoked,
	ActionGuardianPermissionGranted, ActionGuardianPermissionRevoked,
	ActionReportsViewed,
	ActionSessionClaimed, ActionSessionDeleted,
	ActionFileDeleted, ActionFileRestored, ActionFilePurged,
	ActionWorkspaceExported, ActionWorkspaceImported, ActionWorkspaceRecovered,
}

// KnownAction reports whether a name is one this build records.
func KnownAction(action Action) bool { return contains(Actions, action) }
