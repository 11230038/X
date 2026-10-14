package authz

// The store behind the policy: grants, links and the permissions on them.
//
// Three tables and no cleverness. What is deliberate is what is *not* here:
// there is no Update for a link and no soft delete, because revoking is
// deleting the row (§2.3), and there is no read of a link that does not come
// back with its permissions, because a link without them is a link that permits
// nothing and answering "does it exist" separately is how a caller ends up
// treating its existence as permission.

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store is the persistence the authorization service needs. It is declared here
// rather than next to its implementation because the service is what has to be
// testable without a database — the cases about "who may see whose" are cases
// about rules, and a fake that answers them is enough for those.
type Store interface {
	// Preset reads one account's preset. An account that is not there is
	// ErrUnknownUser rather than a zero preset: "this account does not exist"
	// and "this account has no preset" are the same string in the column and
	// two very different things to a caller.
	Preset(ctx context.Context, userID int64) (string, error)
	// SavePreset writes one account's preset. It is the only column of the
	// users table this package writes: role and preset are the two identity
	// layers, and the preset is the one an administrator may move.
	SavePreset(ctx context.Context, userID int64, preset string) error
	// Accounts lists every account with the two identity layers it carries.
	// There is no filter and no page: this is the governance list, the
	// instance's accounts are what it is, and a partial list of them would
	// answer "which accounts exist" with a subset nobody chose.
	Accounts(ctx context.Context) ([]Account, error)
	// Link reads one relationship with the permissions it carries.
	Link(ctx context.Context, guardianID, learnerID int64) (Link, error)
	// LinkByID reads one relationship by its own id, which is what the routes
	// that revoke a permission have.
	LinkByID(ctx context.Context, linkID string) (Link, error)
	// Links lists the relationships one account holds as guardian.
	Links(ctx context.Context, guardianID int64) ([]Link, error)
	// AllLinks lists every relationship in the instance, which is what the
	// administrative view reads. It is a separate method rather than a zero
	// meaning "no filter": a caller that passed an id of zero by mistake would
	// otherwise be handed every relationship there is, and that mistake is one
	// integer away in the caller that reads an id out of a request.
	AllLinks(ctx context.Context) ([]Link, error)
	// SaveLink writes a relationship and its permissions together, or neither.
	SaveLink(ctx context.Context, link Link) error
	// DeleteLink removes a relationship. The permissions go with it through the
	// foreign key's cascade, so a revoked link cannot leave a permission behind
	// for a later link with the same key to inherit.
	DeleteLink(ctx context.Context, linkID string) error
	// SavePermission adds one permission to a link. Adding one the link already
	// carries is not an error: granting twice is what a repeated request looks
	// like, and refusing it would make a retry a failure.
	SavePermission(ctx context.Context, linkID string, permission Permission, grantedBy int64) error
	// DeletePermission removes one permission from a link.
	DeletePermission(ctx context.Context, linkID string, permission Permission) error

	// Assignments lists an account's grants.
	Assignments(ctx context.Context, userID int64) ([]Assignment, error)
	// SaveAssignment writes one grant, replacing the decision already recorded
	// for the same item.
	SaveAssignment(ctx context.Context, assignment Assignment) error
	// DeleteAssignment removes the decision recorded for one item.
	DeleteAssignment(ctx context.Context, userID int64, item Item) error
}

// GORMStore persists the governance tables through GORM.
type GORMStore struct {
	db *gorm.DB
}

// NewGORMStore creates a store over an existing handle.
func NewGORMStore(db *gorm.DB) *GORMStore { return &GORMStore{db: db} }

func (s *GORMStore) ready() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("authz store is not initialized")
	}
	return nil
}

// Preset reads one account's preset.
func (s *GORMStore) Preset(ctx context.Context, userID int64) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	var user models.User
	err := s.db.WithContext(ctx).Select("preset").First(&user, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("%w: %d", ErrUnknownUser, userID)
	}
	if err != nil {
		return "", fmt.Errorf("read account preset: %w", err)
	}
	return user.Preset, nil
}

// SavePreset writes one account's preset.
//
// The update is guarded by the row count rather than by a read first: an update
// that matched nothing and one that wrote the value already there are the same
// number, so the second is looked up only when the count is zero — which is the
// only case where the two can be told apart, and the only case where the answer
// matters.
func (s *GORMStore) SavePreset(ctx context.Context, userID int64, preset string) error {
	if err := s.ready(); err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Model(&models.User{}).
		Where("user_id = ?", userID).
		Update("preset", preset)
	if result.Error != nil {
		return fmt.Errorf("write account preset: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	// Zero rows: either the account is not there, or the preset was already this
	// one. Only the first is an error, and asking is the only way to know.
	var count int64
	if err := s.db.WithContext(ctx).Model(&models.User{}).
		Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return fmt.Errorf("read account for preset write: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: %d", ErrUnknownUser, userID)
	}
	return nil
}

// Accounts lists every account with the two identity layers it carries.
//
// It is ordered by the primary key so that two lists a page apart are the same
// list, and it selects columns by name rather than reading the whole row,
// because the whole row carries the password hash and this projection is the one
// that travels (§12).
func (s *GORMStore) Accounts(ctx context.Context) ([]Account, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var rows []models.User
	err := s.db.WithContext(ctx).
		Select("user_id", "username", "role", "preset", "disable").
		Order("user_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	accounts := make([]Account, 0, len(rows))
	for _, row := range rows {
		accounts = append(accounts, Account{
			UserID:   row.UserID,
			Username: row.Username,
			Role:     row.Role,
			Preset:   row.Preset,
			Disabled: row.Disable,
		})
	}
	return accounts, nil
}

// Link reads one relationship with the permissions it carries.
func (s *GORMStore) Link(ctx context.Context, guardianID, learnerID int64) (Link, error) {
	if err := s.ready(); err != nil {
		return Link{}, err
	}
	var row models.GuardianLink
	err := s.db.WithContext(ctx).
		Where("guardian_id = ? AND learner_id = ?", guardianID, learnerID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Link{}, fmt.Errorf("%w: %d -> %d", ErrLinkNotFound, guardianID, learnerID)
	}
	if err != nil {
		return Link{}, fmt.Errorf("read guardian link: %w", err)
	}
	link := Link{
		ID: row.LinkID, GuardianID: row.GuardianID, LearnerID: row.LearnerID,
		CreatedAt: row.CreatedAt,
	}
	if row.CreatedBy != nil {
		link.CreatedBy = *row.CreatedBy
	}
	permissions, err := s.permissionsOf(ctx, row.LinkID)
	if err != nil {
		return Link{}, err
	}
	link.Permissions = permissions
	return link, nil
}

// Links lists the relationships one account holds as guardian.
func (s *GORMStore) Links(ctx context.Context, guardianID int64) ([]Link, error) {
	return s.links(ctx, "guardian_id = ?", guardianID)
}

// AllLinks lists every relationship.
//
// The ordering matches Links' — written first, and then by id — so that the
// administrative list and a guardian's own list are the same list wherever they
// overlap.
func (s *GORMStore) AllLinks(ctx context.Context) ([]Link, error) {
	return s.links(ctx, "1 = 1")
}

// links is the one read all three of the above are, so that a link cannot come
// back with its permissions through one path and without them through another.
//
// The where clause is passed whole rather than assembled here, and the
// "everything" case is written as a predicate that is true rather than as an
// empty string: an empty condition is not "no filter" to the query builder, it
// is a syntax error, and the difference only shows up in the one call that
// passes nothing.
// LinkByID reads one relationship by its own id.
func (s *GORMStore) LinkByID(ctx context.Context, linkID string) (Link, error) {
	links, err := s.links(ctx, "link_id = ?", linkID)
	if err != nil {
		return Link{}, err
	}
	if len(links) == 0 {
		return Link{}, fmt.Errorf("%w: %s", ErrLinkNotFound, linkID)
	}
	return links[0], nil
}

// links is the one read all three of the above are, so that a link cannot come
// back with its permissions through one path and without them through another.
//
// The where clause is passed whole rather than assembled here, and the
// "everything" case is written as a predicate that is true rather than as an
// empty string: an empty condition is not "no filter" to the query builder, it
// is a syntax error, and the difference would only show up in the one call that
// passes nothing.
func (s *GORMStore) links(ctx context.Context, where string, args ...any) ([]Link, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var rows []models.GuardianLink
	err := s.db.WithContext(ctx).
		Where(where, args...).
		Order("creat_time ASC, link_id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list guardian links: %w", err)
	}
	links := make([]Link, 0, len(rows))
	for _, row := range rows {
		permissions, permErr := s.permissionsOf(ctx, row.LinkID)
		if permErr != nil {
			return nil, permErr
		}
		link := Link{
			ID: row.LinkID, GuardianID: row.GuardianID, LearnerID: row.LearnerID,
			Permissions: permissions, CreatedAt: row.CreatedAt,
		}
		if row.CreatedBy != nil {
			link.CreatedBy = *row.CreatedBy
		}
		links = append(links, link)
	}
	return links, nil
}

func (s *GORMStore) permissionsOf(ctx context.Context, linkID string) ([]Permission, error) {
	var rows []models.GuardianPermission
	err := s.db.WithContext(ctx).
		Where("link_id = ?", linkID).
		Order("permission ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list guardian permissions: %w", err)
	}
	permissions := make([]Permission, 0, len(rows))
	for _, row := range rows {
		permissions = append(permissions, Permission(row.Permission))
	}
	return permissions, nil
}

// SaveLink writes a relationship and its permissions in one transaction.
//
// The two go in together because a link that exists with none of the
// permissions it was created with is a relationship that quietly permits
// nothing, and the caller that has to notice is not the caller that created it.
func (s *GORMStore) SaveLink(ctx context.Context, link Link) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := models.GuardianLink{
			LinkID: link.ID, GuardianID: link.GuardianID, LearnerID: link.LearnerID,
			CreatedBy: data.OwnerPointer(link.CreatedBy),
			CreatedAt: link.CreatedAt,
		}
		if err := tx.Create(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: %d -> %d", ErrLinkExists, link.GuardianID, link.LearnerID)
			}
			return fmt.Errorf("create guardian link: %w", err)
		}
		for _, permission := range link.Permissions {
			record := models.GuardianPermission{
				LinkID:     link.ID,
				Permission: string(permission),
				GrantedBy:  data.OwnerPointer(link.CreatedBy),
				CreatedAt:  link.CreatedAt,
			}
			if err := tx.Create(&record).Error; err != nil {
				return fmt.Errorf("grant %s: %w", permission, err)
			}
		}
		return nil
	})
}

// DeleteLink removes a relationship.
//
// "Already gone" is not an error. Revoking twice is what a retry looks like, and
// the caller's intent — this relationship must not exist — is satisfied either
// way; only a link that was never there at all is worth telling apart, and this
// cannot.
func (s *GORMStore) DeleteLink(ctx context.Context, linkID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	err := s.db.WithContext(ctx).Where("link_id = ?", linkID).Delete(&models.GuardianLink{}).Error
	if err != nil {
		return fmt.Errorf("delete guardian link: %w", err)
	}
	return nil
}

// SavePermission adds one permission to a link.
func (s *GORMStore) SavePermission(ctx context.Context, linkID string, permission Permission, grantedBy int64) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The link is read under a lock first, so that a permission cannot be
		// added to a relationship that is being revoked in another request.
		var row models.GuardianLink
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&row, "link_id = ?", linkID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: %s", ErrLinkNotFound, linkID)
		}
		if err != nil {
			return fmt.Errorf("read guardian link: %w", err)
		}
		var existing models.GuardianPermission
		err = tx.First(&existing, "link_id = ? AND permission = ?", linkID, string(permission)).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read guardian permission: %w", err)
		}
		record := models.GuardianPermission{
			LinkID: linkID, Permission: string(permission), GrantedBy: data.OwnerPointer(grantedBy),
		}
		if err := tx.Create(&record).Error; err != nil {
			return fmt.Errorf("grant %s: %w", permission, err)
		}
		return nil
	})
}

// DeletePermission removes one permission from a link.
func (s *GORMStore) DeletePermission(ctx context.Context, linkID string, permission Permission) error {
	if err := s.ready(); err != nil {
		return err
	}
	result := s.db.WithContext(ctx).
		Where("link_id = ? AND permission = ?", linkID, string(permission)).
		Delete(&models.GuardianPermission{})
	if result.Error != nil {
		return fmt.Errorf("revoke %s: %w", permission, result.Error)
	}
	return nil
}

// Assignments lists an account's grants.
//
// It is not scoped by a caller: a caller asking this question is asking about
// its own two layers, and the account it is asking about came from the token.
// A scoped version of this would answer "this account has no grants" for
// somebody else's account, which is the same string an account with none
// produces.
func (s *GORMStore) Assignments(ctx context.Context, userID int64) ([]Assignment, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var rows []models.UserGrant
	err := s.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("item_kind ASC, item_id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	assignments := make([]Assignment, 0, len(rows))
	for _, row := range rows {
		assignment := Assignment{
			GrantID: row.GrantID, UserID: row.UserID,
			Item:    Item{Kind: Kind(row.ItemKind), ID: row.ItemID},
			Allowed: row.Allowed, CreatedAt: row.CreatedAt,
		}
		if row.GrantedBy != nil {
			assignment.GrantedBy = *row.GrantedBy
		}
		assignments = append(assignments, assignment)
	}
	return assignments, nil
}

// SaveAssignment writes one grant, replacing the decision already recorded for
// the same item.
//
// Read-then-write rather than an upsert, and under a lock, for the reason the
// knowledge base's default mark is: the decision is a row's *content*, so the
// thing to get right is that two administrators writing the same item at the
// same moment end with one row and not a 1062, and a locking read over the
// unique key is a primitive this repository already depends on.
func (s *GORMStore) SaveAssignment(ctx context.Context, assignment Assignment) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND item_kind = ? AND item_id = ?",
				assignment.UserID, string(assignment.Item.Kind), assignment.Item.ID)
		var existing models.UserGrant
		err := scope.First(&existing).Error
		switch {
		case err == nil:
			update := tx.Model(&models.UserGrant{}).
				Where("grant_id = ?", existing.GrantID).
				Updates(map[string]any{
					"allowed":    assignment.Allowed,
					"granted_by": data.OwnerPointer(assignment.GrantedBy),
				})
			if update.Error != nil {
				return fmt.Errorf("update grant: %w", update.Error)
			}
			return nil
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return fmt.Errorf("read grant: %w", err)
		}
		row := models.UserGrant{
			UserID: assignment.UserID, ItemKind: string(assignment.Item.Kind),
			ItemID: assignment.Item.ID, Allowed: assignment.Allowed,
			GrantedBy: data.OwnerPointer(assignment.GrantedBy),
			CreatedAt: assignment.CreatedAt,
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("create grant: %w", err)
		}
		return nil
	})
}

// DeleteAssignment removes the decision recorded for one item.
func (s *GORMStore) DeleteAssignment(ctx context.Context, userID int64, item Item) error {
	if err := s.ready(); err != nil {
		return err
	}
	result := s.db.WithContext(ctx).
		Where("user_id = ? AND item_kind = ? AND item_id = ?", userID, string(item.Kind), item.ID).
		Delete(&models.UserGrant{})
	if result.Error != nil {
		return fmt.Errorf("delete grant: %w", result.Error)
	}
	return nil
}

var _ Store = (*GORMStore)(nil)
