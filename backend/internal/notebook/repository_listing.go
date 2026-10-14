package notebook

import (
	"context"
	"fmt"
	"strings"

	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/gorm"
)

// List returns one page of the bank, newest first.
//
// The order carries a tiebreaker: two questions written in the same second are
// ordinary — a turn generates several in a row — and an order that changes
// between identical requests makes paging skip rows and repeat others.
func (r *GORMRepository) List(ctx context.Context, filter Filter) ([]Entry, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var records []models.NotebookEntry
	query := applyFilter(r.db.WithContext(ctx), filter)
	err := query.
		Order("creat_time DESC, notebook_entries_id ASC").
		Limit(clampLimit(filter.Limit)).
		Offset(max(filter.Offset, 0)).
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("list questions: %w", err)
	}

	entries := make([]Entry, 0, len(records))
	ids := make([]string, 0, len(records))
	for _, record := range records {
		entry, err := toEntry(record)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
		ids = append(ids, record.NotebookEntriesID)
	}

	// One more query for the whole page, rather than one per row: a page of
	// fifty questions filed under two categories each would otherwise be a
	// hundred extra round trips to show a list.
	categories, err := r.categoriesOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	for index := range entries {
		entries[index].Categories = categories[entries[index].ID]
	}
	return entries, nil
}

// applyFilter narrows a query to the questions a filter selects. Every field of
// the filter but the caller is optional; the caller is not, and it is applied
// first.
//
// It is first so that it is the condition the rest are read against, and it is
// unconditional because there is no listing this version serves that spans two
// accounts: a filter that named every field to match somebody else's question
// still returns nothing, because the scope has already excluded it. That is the
// shape §2.3 asks for — ownership is a filter, and a filter that matched nothing
// is an empty list, never a refusal.
func applyFilter(query *gorm.DB, filter Filter) *gorm.DB {
	query = data.OwnerScope(query, filter.Caller.UserID, filter.Caller.IsAdmin(), "owner_id")
	if filter.Source != "" {
		query = query.Where("source = ?", filter.Source)
	}
	if filter.MaterialID != "" {
		query = query.Where("material_id = ?", filter.MaterialID)
	}
	if filter.Difficulty != "" {
		query = query.Where("difficulty = ?", filter.Difficulty)
	}
	if filter.Type != "" {
		query = query.Where("question_type = ?", string(filter.Type))
	}
	if filter.Category != "" {
		query = query.Where(`EXISTS (
			SELECT 1 FROM notebook_entry_categories AS filing
			JOIN notebook_categories AS category
			  ON category.notebook_categories_id = filing.notebook_categories_id
			WHERE filing.notebook_entries_id = notebook_entries.notebook_entries_id
			  AND category.name = ?)`, filter.Category)
	}
	switch filter.Resolution {
	case ResolutionAny:
		// Nothing to add: the three states below are the ones the column has.
	case ResolutionUntouched:
		query = query.Where("resolved IS NULL")
	case ResolutionOpen:
		query = query.Where("resolved IS NOT NULL AND resolved = ?", false)
	case ResolutionDone:
		query = query.Where("resolved IS NOT NULL AND resolved = ?", true)
	}
	if filter.Bookmarked != nil {
		// Asked for as the two halves of the column rather than as equality with
		// the value. A question nobody has marked is NULL, and an equality
		// against FALSE would leave those rows in neither half: a client paging
		// through "not bookmarked" would lose them and be told nothing. Written
		// this way the two values partition the bank, and the negative one is the
		// complement of the positive one.
		if *filter.Bookmarked {
			query = query.Where("bookmarked = TRUE")
		} else {
			query = query.Where("bookmarked IS NOT TRUE")
		}
	}
	if filter.Query != "" {
		// The collation is binary, so "case-insensitive" has to be asked for.
		// The pattern is a bound parameter, and the wildcards a user may have
		// typed in it are escaped, so that searching for "50%" looks for that
		// text rather than for anything starting with 50.
		//
		// The escape character is spelled with four backslashes in Go and two in
		// the statement: what this engine has to be told is that the escape
		// character is a backslash, and a single one before the closing quote is
		// an unterminated string literal — it does not even parse, which is how
		// this was found.
		query = query.Where(`LOWER(question) LIKE ? ESCAPE '\\'`,
			"%"+escapeLike(strings.ToLower(filter.Query))+"%")
	}
	return query
}

// escapeLike makes a user's text safe to put inside a LIKE pattern.
func escapeLike(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, character := range text {
		if character == '\\' || character == '%' || character == '_' {
			builder.WriteRune('\\')
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return DefaultListLimit
	}
	return min(limit, MaxListLimit)
}
