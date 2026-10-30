package service

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/lib/logger"
	"gorkycode/backend/internal/model"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// MeetingOverlapFinder finds the user's stored meetings that overlap a period.
type MeetingOverlapFinder interface {
	FindOverlappingMeetings(ctx context.Context, userID uuid.UUID, start, end time.Time, duration time.Duration) ([]model.Task, error)
}

// GoogleBusyChecker reports whether the user's Google Calendar is busy in a period.
type GoogleBusyChecker interface {
	GoogleBusy(ctx context.Context, userID uuid.UUID, start, end time.Time) (bool, error)
}

// MeetingConflict describes what a new meeting overlaps. Title is empty when
// only Google Calendar reported the period as busy.
type MeetingConflict struct {
	Title string
}

// Message explains why the meeting was not created.
func (c MeetingConflict) Message() string {
	if c.Title == "" {
		return "Это время уже занято в Google Календаре. Выберите другое время."
	}
	return fmt.Sprintf("Это время уже занято встречей «%s». Выберите другое время.", c.Title)
}

// MeetingConflicts checks a new meeting against stored meetings and Google Calendar.
type MeetingConflicts struct {
	meetings     MeetingOverlapFinder
	google       GoogleBusyChecker
	log          *slog.Logger
	queryTimeout time.Duration
	duration     time.Duration
}

// NewMeetingConflicts constructs the conflict check. A nil google skips the
// Google Calendar check; duration is the length of every meeting.
func NewMeetingConflicts(
	meetings MeetingOverlapFinder,
	google GoogleBusyChecker,
	log *slog.Logger,
	queryTimeout, duration time.Duration,
) *MeetingConflicts {
	return &MeetingConflicts{
		meetings:     meetings,
		google:       google,
		log:          log,
		queryTimeout: queryTimeout,
		duration:     duration,
	}
}

// Find returns the first conflict of a meeting that starts at start, or nil.
// start must be in the user's time zone. A failed check of a connected
// Google Calendar prevents creation because availability is unknown.
func (s *MeetingConflicts) Find(ctx context.Context, userID uuid.UUID, start time.Time) (*MeetingConflict, error) {
	end := start.Add(s.duration)

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	meetings, err := s.meetings.FindOverlappingMeetings(queryCtx, userID, start, end, s.duration)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("find overlapping meetings: %w", err)
	}
	if len(meetings) > 0 {
		return &MeetingConflict{Title: meetings[0].Title}, nil
	}

	if s.google == nil {
		return nil, nil
	}
	busy, err := s.google.GoogleBusy(ctx, userID, start, end)
	if err != nil {
		s.log.Warn("failed to check google calendar for conflicts",
			slog.String("user_id", userID.String()),
			logger.Err(err),
		)
		return nil, fmt.Errorf("%w: check google calendar availability: %w", model.ErrServiceUnavailable, err)
	}
	if busy {
		return &MeetingConflict{}, nil
	}
	return nil, nil
}
