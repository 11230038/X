package mastery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/authz"
	"gorm.io/gorm"
)

// mastery_path_sessions, read from the session side.
//
// The table is written by TouchSession, which lives in repository_path.go
// because it is one clause of a tree change and runs in the same transaction as
// one. The reads here are the other direction: nothing writes them, and they are
// what turns a conversation into a path.

// PathOfSession reads the newest link between a conversation and a path.
//
// The order is the index's own — idx_mastery_path_sessions_session_seen is
// (session_id, last_seen_time DESC, path_id) — and the third column is part of
// it rather than decoration: two paths touched in the same second, which is
// exactly what a turn that switches paths writes, would otherwise come back in
// whatever order the engine chose. The path id breaks that tie the same way on
// every read, so "where was I" is a question with one answer rather than two.
//
// A NULL last_seen_time sorts last under a DESC order on seekdb, which is the
// reading this wants: a row written by a build that did not set the time is a
// link nobody has confirmed, and the newest confirmed one is the answer.
func (r *GORMRepository) PathOfSession(ctx context.Context, sessionID string, caller authz.Caller) (string, error) {
	if err := r.ready(); err != nil {
		return "", err
	}
	var row struct {
		PathID string `gorm:"column:path_id"`
	}
	// A link is visible only when the path it points at is the caller's: the link
	// has no owner of its own, so the subquery over owned paths is the whole of
	// the narrowing.
	err := r.db.WithContext(ctx).Table("mastery_path_sessions").
		Select("path_id").
		Where("session_id = ?", sessionID).
		Where("path_id IN (?)", pathScope(r.db.WithContext(ctx), caller)).
		Order("last_seen_time DESC, path_id ASC").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("%w: session %s is on no path", ErrNotFound, sessionID)
	}
	if err != nil {
		return "", fmt.Errorf("read the path of session %s: %w", sessionID, err)
	}
	return row.PathID, nil
}

// ActivePathsOfSession reads the paths a conversation is still working on, most
// recently seen first.
//
// The join is what makes "still working on" mean something: the link table says
// a conversation has been on a path, and mastery_topic_meta says whether the
// path is still active. A LEFT JOIN with a COALESCE rather than an inner one,
// because a path whose metadata row is missing is treated as active everywhere
// else in this package (Service.Load reports such a path rather than refusing
// it), and a count that quietly left those out would be a count of the wrong
// thing.
func (r *GORMRepository) ActivePathsOfSession(ctx context.Context, sessionID string, caller authz.Caller) ([]string, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []struct {
		PathID string `gorm:"column:path_id"`
	}
	err := r.db.WithContext(ctx).Table("mastery_path_sessions AS s").
		Select("s.path_id AS path_id").
		Joins("LEFT JOIN mastery_topic_meta AS m ON m.path_id = s.path_id").
		Where("s.session_id = ? AND COALESCE(m.status, ?) = ?", sessionID, StatusActive, StatusActive).
		Where("s.path_id IN (?)", pathScope(r.db.WithContext(ctx), caller)).
		Order("s.last_seen_time DESC, s.path_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read the active paths of session %s: %w", sessionID, err)
	}
	paths := make([]string, 0, len(rows))
	for _, row := range rows {
		paths = append(paths, row.PathID)
	}
	return paths, nil
}

// SessionsOn reads the conversations linked to a path, most recently seen first.
func (r *GORMRepository) SessionsOn(ctx context.Context, pathID string, caller authz.Caller) ([]string, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []struct {
		SessionID string `gorm:"column:session_id"`
	}
	err := r.db.WithContext(ctx).Table("mastery_path_sessions").
		Select("session_id").
		Where("path_id = ?", pathID).
		Where("path_id IN (?)", pathScope(r.db.WithContext(ctx), caller)).
		Order("last_seen_time DESC, session_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read the sessions of path %s: %w", pathID, err)
	}
	sessions := make([]string, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, row.SessionID)
	}
	return sessions, nil
}

// SeenAt reads one link's last_seen_time.
func (r *GORMRepository) SeenAt(ctx context.Context, pathID, sessionID string, caller authz.Caller) (time.Time, error) {
	if err := r.ready(); err != nil {
		return time.Time{}, err
	}
	var row struct {
		LastSeen *time.Time `gorm:"column:last_seen_time"`
	}
	err := r.db.WithContext(ctx).Table("mastery_path_sessions").
		Select("last_seen_time").
		Where("path_id = ? AND session_id = ?", pathID, sessionID).
		Where("path_id IN (?)", pathScope(r.db.WithContext(ctx), caller)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, fmt.Errorf("%w: session %s is not on path %s", ErrNotFound, sessionID, pathID)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read when session %s last saw path %s: %w", sessionID, pathID, err)
	}
	if row.LastSeen == nil {
		// A link with no time is one this package has not written. It is not an
		// error and it is not a time either, and the zero time is what "not known"
		// is spelled as everywhere else in Go.
		return time.Time{}, nil
	}
	return *row.LastSeen, nil
}
