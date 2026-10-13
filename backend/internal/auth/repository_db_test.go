package auth

// The account half of the storage contract, checked against a real seekdb.
//
// Two of these are the reason this file exists at all. The unique index on
// `username` is what refuses a second account with a name, and the adapter only
// reports that correctly if GORM translates the database's duplicate-key error
// — a mapping that was asserted against a hand-made error until now, which is
// the same as asserting it against nothing. And the column's collation is what
// makes a username case-sensitive; the fake repository in service_test.go has
// an opinion about case that is really the schema's.
//
// Nothing here calls t.Parallel: one database per package, empty at each test.

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

func newDBRepository(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "auth")
	testdb.Reset(t, resource, "users")
	return NewGORMRepository(resource.GORM()), resource
}

// passwordHash stands in for a stored digest. Nothing here checks a password —
// that is the service's half — so the value only has to be a non-empty string.
const passwordHash = "$2a$10$abcdefghijklmnopqrstuv"

func register(t *testing.T, repo *GORMRepository, username string) Record {
	t.Helper()
	record, err := repo.Create(context.Background(), CreateParams{
		Username: username, PasswordHash: passwordHash,
	})
	if err != nil {
		t.Fatalf("create the account %q: %v", username, err)
	}
	return record
}

func countRows(t *testing.T, resource *data.Resource, table string) int64 {
	t.Helper()
	var count int64
	err := resource.GORM().Raw("SELECT COUNT(*) FROM `" + table + "`").Scan(&count).Error
	if err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return count
}

// storedAccount reads the columns a registration writes, from the row itself:
// what the adapter says it wrote and what the table holds are two answers, and
// the table's is the one that lasts.
type storedAccount struct {
	UserID         int64  `gorm:"column:user_id"`
	Username       string `gorm:"column:username"`
	Role           string `gorm:"column:role"`
	Preset         string `gorm:"column:preset"`
	Avatar         string `gorm:"column:avatar"`
	Disable        bool   `gorm:"column:disable"`
	LearnerProfile string `gorm:"column:learner_profile"`
	Extra          string `gorm:"column:extra"`
}

func readAccount(t *testing.T, resource *data.Resource, username string) storedAccount {
	t.Helper()
	var row storedAccount
	err := resource.GORM().Raw("SELECT user_id, username, role, preset, avatar, disable, "+
		"learner_profile, extra FROM users WHERE username = ?", username).Scan(&row).Error
	if err != nil {
		t.Fatalf("read the account %q: %v", username, err)
	}
	return row
}

func TestDBASecondAccountWithTheSameNameIsRefused(t *testing.T) {
	repo, resource := newDBRepository(t)
	first := register(t, repo, "alice")
	if first.User.UserID == 0 {
		t.Fatal("the stored account has no id, want the one the database assigned")
	}

	// The name is refused by the table's unique index, and the adapter reports
	// that as a name that is taken. The translation is GORM's, switched on by
	// TranslateError where the handle is opened; if it were off, this error
	// would arrive as a storage failure and every caller of ErrUsernameTaken
	// would silently be reporting the wrong thing.
	_, err := repo.Create(context.Background(), CreateParams{
		Username: "alice", PasswordHash: passwordHash,
	})
	if !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("registering a name twice: %v, want %v", err, ErrUsernameTaken)
	}
	// And the name taken is the whole of what it says: the database's own
	// wording is not carried up in it.
	if text := err.Error(); text != ErrUsernameTaken.Error() {
		t.Errorf("the refusal reads %q, want no storage detail in it", text)
	}
	if got := countRows(t, resource, "users"); got != 1 {
		t.Errorf("%d accounts are stored, want the first one only", got)
	}

	// The account that did get stored is the one the caller asked for, with the
	// id the database assigned rather than one the adapter made up.
	if first.User.Username != "alice" || first.User.Role != defaultRole {
		t.Errorf("the stored account is %+v, want alice as a %s", first.User, defaultRole)
	}
}

func TestDBTheNameIsComparedByteForByte(t *testing.T) {
	repo, resource := newDBRepository(t)
	upper := register(t, repo, "Alice")
	lower := register(t, repo, "alice")

	// Two accounts, not one. The migration's header says why: seekdb defaults to
	// utf8mb4_general_ci, under which these are one name and the second
	// registration would be refused as taken.
	if upper.User.UserID == lower.User.UserID {
		t.Fatalf("both spellings are account %d, want two accounts", upper.User.UserID)
	}
	if got := countRows(t, resource, "users"); got != 2 {
		t.Errorf("%d accounts are stored, want both spellings", got)
	}

	// Each spelling finds its own account, on both read paths, and a spelling
	// that was never registered finds none — which is the same fact read from
	// the other side, and the one a login depends on.
	for _, scenario := range []struct {
		username string
		want     int64
	}{
		{"Alice", upper.User.UserID},
		{"alice", lower.User.UserID},
	} {
		found, err := repo.FindByUsername(context.Background(), scenario.username)
		if err != nil {
			t.Fatalf("find %q: %v", scenario.username, err)
		}
		if found.User.UserID != scenario.want {
			t.Errorf("%q is account %d, want %d", scenario.username, found.User.UserID, scenario.want)
		}
		byID, err := repo.FindByID(context.Background(), scenario.want)
		if err != nil {
			t.Fatalf("find account %d: %v", scenario.want, err)
		}
		if byID.User.Username != scenario.username {
			t.Errorf("account %d is called %q, want %q", scenario.want, byID.User.Username, scenario.username)
		}
	}
	if _, err := repo.FindByUsername(context.Background(), "ALICE"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("a name nobody registered: %v, want %v", err, ErrUserNotFound)
	}
}

func TestDBANewAccountHasEveryColumnTheTableDemands(t *testing.T) {
	repo, resource := newDBRepository(t)
	register(t, repo, "alice")

	// Every column in this table is NOT NULL, and two of them are JSON with no
	// default, so a registration that leaves them to GORM's zero values is
	// refused by the database rather than stored half-filled. What it stores
	// instead is what the adapter writes explicitly, and this is that list read
	// back.
	row := readAccount(t, resource, "alice")
	if row.Role != defaultRole {
		t.Errorf("the account has role %q, want %q", row.Role, defaultRole)
	}
	if row.Preset != "" || row.Avatar != "" {
		t.Errorf("the account has preset %q and avatar %q, want both empty", row.Preset, row.Avatar)
	}
	if row.Disable {
		t.Error("a freshly created account is disabled, want it usable")
	}
	if row.LearnerProfile != "{}" || row.Extra != "{}" {
		t.Errorf("the account has learner_profile %s and extra %s, want empty objects",
			row.LearnerProfile, row.Extra)
	}

	// A name or a password that is blank is refused by the table's own checks.
	// The service rejects both before reaching here, so this is the backstop —
	// and it is the reason an account nobody can sign into cannot be stored by
	// any other route either.
	blank := []struct{ name, query string }{
		{"a blank name",
			"INSERT INTO users (username, password, learner_profile, extra) " +
				"VALUES ('   ', 'hash', '{}', '{}')"},
		{"a blank password",
			"INSERT INTO users (username, password, learner_profile, extra) " +
				"VALUES ('bob', '', '{}', '{}')"},
	}
	for _, scenario := range blank {
		if err := testdb.Try(resource, scenario.query); err == nil {
			t.Errorf("%s was accepted, want the schema to refuse it", scenario.name)
		}
	}

	// The same refusal through the adapter is a storage failure rather than a
	// domain error, which is what it is: nothing about the caller's input is
	// known to be wrong at this layer.
	if _, err := repo.Create(context.Background(), CreateParams{
		Username: "   ", PasswordHash: passwordHash,
	}); err == nil {
		t.Error("an account with a blank name was created")
	}
	if got := countRows(t, resource, "users"); got != 1 {
		t.Errorf("%d accounts are stored, want the one that was asked for", got)
	}
}

func TestDBAnAccountIsFoundByWhatItIsFlaggedWith(t *testing.T) {
	repo, resource := newDBRepository(t)
	created := register(t, repo, "alice")

	// The adapter reads the three columns a sign-in decides on: the name, the
	// hash and the flag. The hash comes back with the row — it is the service
	// that keeps it out of anything a caller sees.
	found, err := repo.FindByUsername(context.Background(), "alice")
	if err != nil {
		t.Fatalf("find the account: %v", err)
	}
	if found.PasswordHash != passwordHash || found.Disabled {
		t.Errorf("the account reads %+v, want the hash and no disable flag", found)
	}

	// The flag is read, not assumed: an account the database says is disabled
	// comes back disabled, which is what stops a login at the door.
	if err := testdb.Try(resource, "UPDATE users SET disable = TRUE WHERE username = 'alice'"); err != nil {
		t.Fatalf("disable the account: %v", err)
	}
	disabled, err := repo.FindByID(context.Background(), created.User.UserID)
	if err != nil {
		t.Fatalf("find the disabled account: %v", err)
	}
	if !disabled.Disabled {
		t.Error("the account is not reported as disabled, want the stored flag")
	}

	// A row that is not there is reported as missing on both reads, which is the
	// answer the service turns into "no such account" without saying so.
	if _, err := repo.FindByUsername(context.Background(), "nobody"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("finding a name nobody registered: %v, want %v", err, ErrUserNotFound)
	}
	if _, err := repo.FindByID(context.Background(), created.User.UserID+1000); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("finding an id nobody holds: %v, want %v", err, ErrUserNotFound)
	}
}

// The registration time is the database's when a caller does not supply one,
// and the two columns agree: nothing in this package writes them, so a check
// that they are filled at all is a check that the table's defaults hold.
func TestDBANewAccountIsStampedByTheTable(t *testing.T) {
	repo, resource := newDBRepository(t)
	register(t, repo, "alice")

	var row struct {
		CreatedAt time.Time `gorm:"column:creat_time"`
		UpdatedAt time.Time `gorm:"column:update_time"`
	}
	err := resource.GORM().Raw(
		"SELECT creat_time, update_time FROM users WHERE username = 'alice'").Scan(&row).Error
	if err != nil {
		t.Fatalf("read the times: %v", err)
	}
	if row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
		t.Errorf("the account was stamped %v and %v, want both written", row.CreatedAt, row.UpdatedAt)
	}
}
