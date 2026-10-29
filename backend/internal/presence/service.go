package presence

import (
	"context"
	"errors"
	"time"

	"backend/internal/authz"
)

// Summary is the home page's two attendance numbers.
//
// YesterdayMinutes rides along because the card that shows today's time says
// something about it — the demo's line is a comparison — and asking for it
// here costs one COUNT over a window the table is already indexed for. A
// client that only wants today's number ignores it.
type Summary struct {
	TodayMinutes     int
	YesterdayMinutes int
	// StreakDays is how many days in a row the app was open, counting back
	// from today. A day that has not been lived yet does not break the streak,
	// so the count starts at yesterday when today has no minute in it yet.
	StreakDays int
}

// Options is what the service is built with.
type Options struct {
	// Now is the clock, injectable so that a test can stand on a day boundary.
	Now func() time.Time
}

// Service reads and writes one account's presence.
type Service struct {
	repository *Repository
	now        func() time.Time
}

// NewService builds the service.
func NewService(repository *Repository, options Options) (*Service, error) {
	if repository == nil {
		return nil, errors.New("presence: repository is required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, now: now}, nil
}

// Touch records that the caller is here now, and answers with the day so far.
//
// The answer is the point of the write: a browser beats once a minute to stay
// counted, and the same response is what the home page draws, so a beat costs
// one request rather than a beat and a read.
func (s *Service) Touch(ctx context.Context, caller authz.Caller) (Summary, error) {
	if !caller.Valid() {
		return Summary{}, errors.New("presence: caller has no account")
	}
	now := s.now()
	if err := s.repository.Touch(ctx, caller.UserID, now); err != nil {
		return Summary{}, err
	}
	return s.summary(ctx, caller.UserID, now)
}

// Read answers the same question without writing anything.
func (s *Service) Read(ctx context.Context, caller authz.Caller) (Summary, error) {
	if !caller.Valid() {
		return Summary{}, errors.New("presence: caller has no account")
	}
	return s.summary(ctx, caller.UserID, s.now())
}

func (s *Service) summary(ctx context.Context, ownerID int64, now time.Time) (Summary, error) {
	today := midnight(now)
	todayMinutes, err := s.repository.MinutesBetween(ctx, ownerID, today, today.AddDate(0, 0, 1))
	if err != nil {
		return Summary{}, err
	}
	yesterdayMinutes, err := s.repository.MinutesBetween(ctx, ownerID, today.AddDate(0, 0, -1), today)
	if err != nil {
		return Summary{}, err
	}
	days, err := s.repository.DaysSince(ctx, ownerID, today.AddDate(0, 0, -streakLookback))
	if err != nil {
		return Summary{}, err
	}
	return Summary{
		TodayMinutes:     todayMinutes,
		YesterdayMinutes: yesterdayMinutes,
		StreakDays:       streakDays(days, today),
	}, nil
}

// streakLookback is how far back a streak is looked for.
//
// A streak longer than a year is still a streak; it is reported as a year and
// not as a number nobody can verify. The bound exists so that the query has a
// window at all — "every day this account ever opened the app" is the same
// question with a table scan for an answer.
const streakLookback = 365

func midnight(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
}

// streakDays counts consecutive days ending today, or ending yesterday when
// today has no minute in it yet.
//
// The second case is what makes the number honest in the morning: a person who
// was here yesterday and has not opened the app yet today has a streak of one,
// not zero. The day they miss in full is the day it drops to zero.
func streakDays(days []time.Time, today time.Time) int {
	seen := make(map[string]struct{}, len(days))
	for _, day := range days {
		seen[day.Format(time.DateOnly)] = struct{}{}
	}
	cursor := today
	if _, ok := seen[cursor.Format(time.DateOnly)]; !ok {
		cursor = cursor.AddDate(0, 0, -1)
	}
	count := 0
	for {
		if _, ok := seen[cursor.Format(time.DateOnly)]; !ok {
			return count
		}
		count++
		cursor = cursor.AddDate(0, 0, -1)
	}
}
