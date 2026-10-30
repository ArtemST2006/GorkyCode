package service

import (
	"context"
	"errors"
	"gorkycode/backend/internal/model"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeMeetingFinder returns the configured meetings and records the period.
type fakeMeetingFinder struct {
	meetings []model.Task
	err      error

	start, end  time.Time
	duration    time.Duration
	hasDeadline bool
}

func (f *fakeMeetingFinder) FindOverlappingMeetings(
	ctx context.Context,
	_ uuid.UUID,
	start, end time.Time,
	duration time.Duration,
) ([]model.Task, error) {
	_, f.hasDeadline = ctx.Deadline()
	f.start, f.end, f.duration = start, end, duration
	return f.meetings, f.err
}

// fakeGoogleBusy returns the configured answer and records whether it was asked.
type fakeGoogleBusy struct {
	busy  bool
	err   error
	asked bool
}

func (f *fakeGoogleBusy) GoogleBusy(context.Context, uuid.UUID, time.Time, time.Time) (bool, error) {
	f.asked = true
	return f.busy, f.err
}

func TestMeetingConflictsFind(t *testing.T) {
	userID := uuid.New()
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 21, 15, 0, 0, 0, moscow)

	for _, tc := range []struct {
		name         string
		finder       *fakeMeetingFinder
		google       *fakeGoogleBusy
		wantConflict *MeetingConflict
		wantErr      bool
		wantGoogle   bool
	}{
		{
			name:         "stored meeting",
			finder:       &fakeMeetingFinder{meetings: []model.Task{{Title: "Встреча с Иваном"}}},
			google:       &fakeGoogleBusy{busy: true},
			wantConflict: &MeetingConflict{Title: "Встреча с Иваном"},
		},
		{
			name:         "google busy",
			finder:       &fakeMeetingFinder{},
			google:       &fakeGoogleBusy{busy: true},
			wantConflict: &MeetingConflict{},
			wantGoogle:   true,
		},
		{name: "free", finder: &fakeMeetingFinder{}, google: &fakeGoogleBusy{}, wantGoogle: true},
		{name: "google failure blocks creation", finder: &fakeMeetingFinder{}, google: &fakeGoogleBusy{err: errors.New("timeout")}, wantGoogle: true, wantErr: true},
		{name: "without google", finder: &fakeMeetingFinder{}},
		{name: "storage failure", finder: &fakeMeetingFinder{err: errors.New("database unavailable")}, google: &fakeGoogleBusy{}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var google GoogleBusyChecker
			if tc.google != nil {
				google = tc.google
			}
			conflicts := NewMeetingConflicts(tc.finder, google, discardLogger(), time.Second, time.Hour)

			got, err := conflicts.Find(t.Context(), userID, start)

			if (err != nil) != tc.wantErr {
				t.Fatalf("Find() error = %v, wantErr %v", err, tc.wantErr)
			}
			if (got == nil) != (tc.wantConflict == nil) || (got != nil && *got != *tc.wantConflict) {
				t.Fatalf("Find() = %+v, want %+v", got, tc.wantConflict)
			}
			if tc.google != nil && tc.google.asked != tc.wantGoogle {
				t.Fatalf("google asked = %v, want %v", tc.google.asked, tc.wantGoogle)
			}
			if !tc.finder.start.Equal(start) || !tc.finder.end.Equal(start.Add(time.Hour)) || tc.finder.duration != time.Hour {
				t.Fatalf("period = [%s, %s) duration %s", tc.finder.start, tc.finder.end, tc.finder.duration)
			}
			if !tc.finder.hasDeadline {
				t.Fatal("storage call must have a deadline")
			}
		})
	}
}

func TestMeetingConflictMessage(t *testing.T) {
	if got := (MeetingConflict{Title: "Встреча с Иваном"}).Message(); got != "Это время уже занято встречей «Встреча с Иваном». Выберите другое время." {
		t.Fatalf("Message()=%q", got)
	}
	if got := (MeetingConflict{}).Message(); got != "Это время уже занято в Google Календаре. Выберите другое время." {
		t.Fatalf("Message()=%q", got)
	}
}
