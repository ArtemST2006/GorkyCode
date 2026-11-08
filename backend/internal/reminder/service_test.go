package reminder

import (
	"context"
	"errors"
	"gorkycode/backend/internal/config"
	"gorkycode/backend/internal/model"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

var _ Syncer = (*Service)(nil)

var (
	testUserID = uuid.MustParse("7eef3f0d-4469-4f34-9f66-79d1112c2b7c")
	testTaskID = uuid.MustParse("a1e39b02-5346-4b53-a792-e4fcf5121c79")
)

// fakeScheduleStore records every scheduled task.
type fakeScheduleStore struct {
	moments   map[uuid.UUID][]model.Notification
	tasks     []model.TaskWithTimezone
	listLimit int
	alwaysErr bool
}

func (f *fakeScheduleStore) ScheduleTask(
	_ context.Context,
	_ uuid.UUID,
	taskID uuid.UUID,
	moments []model.Notification,
) error {
	if f.alwaysErr {
		return errors.New("database down")
	}
	if f.moments == nil {
		f.moments = map[uuid.UUID][]model.Notification{}
	}
	f.moments[taskID] = moments
	return nil
}

func (f *fakeScheduleStore) UpcomingTasksWithTimezone(
	_ context.Context, _, _ time.Time, limit int,
) ([]model.TaskWithTimezone, error) {
	f.listLimit = limit
	return f.tasks, nil
}

type fakeTimezones struct {
	zone string
	err  error
}

func (f *fakeTimezones) GetUserTimezone(_ context.Context, _ uuid.UUID) (string, error) {
	return f.zone, f.err
}

func testServiceLogs() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// taskOf builds an active dated task with the given record type.
func taskOf(typ string, date time.Time, clock *string) model.Task {
	return model.Task{
		ID: testTaskID, UserID: testUserID, Type: typ,
		Title: "Обратный отсчёт", Date: &date, Time: clock, Status: "active",
	}
}

func moscowLocation(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func TestUpcomingMomentsBuildsContractText(t *testing.T) {
	location := moscowLocation(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, location)
	task := taskOf("reminder", time.Date(2026, 11, 2, 0, 0, 0, 0, location), new("09:30"))

	moments := upcomingMoments(task, "Europe/Moscow", now)
	if len(moments) != 1 {
		t.Fatalf("moments = %d, want 1", len(moments))
	}
	got := moments[0]
	if got.Kind != model.NotificationKindReminderDue {
		t.Errorf("kind = %q, want %q", got.Kind, model.NotificationKindReminderDue)
	}
	if want := time.Date(2026, 11, 2, 9, 30, 0, 0, location); !got.DueAt.Equal(want) {
		t.Errorf("due_at = %v, want %v", got.DueAt, want)
	}
	if want := "Напоминание «Обратный отсчёт» назначено на 09:30."; got.Message != want {
		t.Errorf("message = %q, want %q", got.Message, want)
	}
}

func TestUpcomingMomentsKindPerRecordType(t *testing.T) {
	location := moscowLocation(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, location)

	tests := []struct {
		typ  string
		kind string
		text string
	}{
		{"meeting", model.NotificationKindMeetingStart, "Встреча «Обратный отсчёт» начинается сейчас."},
		{"task", model.NotificationKindTaskDue, "Срок задачи «Обратный отсчёт» наступил."},
	}
	for _, tc := range tests {
		task := taskOf(tc.typ, time.Date(2026, 11, 2, 0, 0, 0, 0, location), new("12:00"))
		moments := upcomingMoments(task, "Europe/Moscow", now)
		if len(moments) != 1 {
			t.Fatalf("%s: moments = %d, want 1", tc.typ, len(moments))
		}
		if moments[0].Kind != tc.kind {
			t.Errorf("%s: kind = %q, want %q", tc.typ, moments[0].Kind, tc.kind)
		}
		if moments[0].Message != tc.text {
			t.Errorf("%s: message = %q, want %q", tc.typ, moments[0].Message, tc.text)
		}
	}
}

func TestUpcomingMomentsDefaultsToNineOClock(t *testing.T) {
	location := moscowLocation(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, location)
	task := taskOf("task", time.Date(2026, 11, 2, 0, 0, 0, 0, location), nil)

	moments := upcomingMoments(task, "Europe/Moscow", now)
	if len(moments) != 1 {
		t.Fatalf("moments = %d, want 1", len(moments))
	}
	want := time.Date(2026, 11, 2, model.TaskDueByDefaultHour, 0, 0, 0, location)
	if !moments[0].DueAt.Equal(want) {
		t.Errorf("due_at = %v, want %v", moments[0].DueAt, want)
	}
	if moments[0].Message != "Срок задачи «Обратный отсчёт» наступает сегодня." {
		t.Errorf("unexpected message %q", moments[0].Message)
	}
}

func TestUpcomingMomentsSkipsPastCompletedUndatedAndUnknown(t *testing.T) {
	location := moscowLocation(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, location)

	tests := []struct {
		name string
		task model.Task
	}{
		{
			name: "past moment",
			task: taskOf("reminder", time.Date(2026, 10, 30, 0, 0, 0, 0, location), new("09:00")),
		},
		{
			name: "exactly now is not scheduled again",
			task: taskOf("reminder", time.Date(2026, 10, 30, 0, 0, 0, 0, location), new("12:00")),
		},
	}
	for _, tc := range tests {
		if moments := upcomingMoments(tc.task, "Europe/Moscow", now); len(moments) != 0 {
			t.Errorf("%s: got %d moments", tc.name, len(moments))
		}
	}

	completed := taskOf("reminder", time.Date(2026, 11, 2, 0, 0, 0, 0, location), new("09:00"))
	completed.Status = "completed"
	if moments := upcomingMoments(completed, "Europe/Moscow", now); len(moments) != 0 {
		t.Errorf("completed task got %d moments", len(moments))
	}

	undated := taskOf("reminder", time.Date(2026, 11, 2, 0, 0, 0, 0, location), new("09:00"))
	undated.Date = nil
	if moments := upcomingMoments(undated, "Europe/Moscow", now); len(moments) != 0 {
		t.Errorf("undated task got %d moments", len(moments))
	}

	unknownType := taskOf("note", time.Date(2026, 11, 2, 0, 0, 0, 0, location), new("09:00"))
	if moments := upcomingMoments(unknownType, "Europe/Moscow", now); len(moments) != 0 {
		t.Errorf("unknown type got %d moments", len(moments))
	}
}

func TestUpcomingMomentsFallsBackToUTCForUnknownZone(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	task := taskOf("task", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), nil)

	moments := upcomingMoments(task, "Mars/Olympus", now)
	if len(moments) != 1 {
		t.Fatalf("moments = %d, want 1", len(moments))
	}
	want := time.Date(2026, 11, 2, model.TaskDueByDefaultHour, 0, 0, 0, time.UTC)
	if !moments[0].DueAt.Equal(want) {
		t.Errorf("due_at = %v, want %v", moments[0].DueAt, want)
	}
}

func TestUpcomingMomentsCarriesTaskIdentity(t *testing.T) {
	location := moscowLocation(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, location)
	task := taskOf("meeting", time.Date(2026, 11, 3, 0, 0, 0, 0, location), new("15:00"))

	moments := upcomingMoments(task, "Europe/Moscow", now)
	if len(moments) != 1 {
		t.Fatalf("moments = %d, want 1", len(moments))
	}
	if moments[0].UserID != task.UserID || moments[0].TaskID != task.ID {
		t.Errorf("identity mismatch: %+v", moments[0])
	}
	if moments[0].Title != task.Title {
		t.Errorf("title = %q, want %q", moments[0].Title, task.Title)
	}
}

// setTestOffsets installs the production advance chain for one test.
func setTestOffsets(t *testing.T) {
	t.Helper()
	setConfiguredOffsets([]time.Duration{72 * time.Hour, 24 * time.Hour, 3 * time.Hour, 10 * time.Minute})
	t.Cleanup(func() { setConfiguredOffsets(nil) })
}

func TestUpcomingMomentsSameDayFiresOnce(t *testing.T) {
	setTestOffsets(t)
	location := moscowLocation(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, location)
	// «Напомни через час»: момент сегодня, предварительный пинг за 10
	// минут не нужен.
	task := taskOf("reminder", time.Date(2026, 10, 30, 0, 0, 0, 0, location), new("13:00"))

	moments := upcomingMoments(task, "Europe/Moscow", now)
	if len(moments) != 1 {
		t.Fatalf("moments = %d, want 1", len(moments))
	}
	want := time.Date(2026, 10, 30, 13, 0, 0, 0, location)
	if !moments[0].DueAt.Equal(want) {
		t.Errorf("due_at = %v, want %v", moments[0].DueAt, want)
	}
	if moments[0].Kind != model.NotificationKindReminderDue {
		t.Errorf("kind = %q, want %q", moments[0].Kind, model.NotificationKindReminderDue)
	}
}

func TestUpcomingMomentsNextDayKeepsAdvanceChain(t *testing.T) {
	setTestOffsets(t)
	location := moscowLocation(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, location)
	task := taskOf("reminder", time.Date(2026, 10, 31, 0, 0, 0, 0, location), new("09:00"))

	moments := upcomingMoments(task, "Europe/Moscow", now)
	// Базовый момент идёт первым, за ним предварительные: 72h и 24h уже в
	// прошлом, остаются 3 ч и 10 мин.
	if len(moments) != 3 {
		t.Fatalf("moments = %d, want 3", len(moments))
	}
	wantDue := []time.Time{
		time.Date(2026, 10, 31, 9, 0, 0, 0, location),
		time.Date(2026, 10, 31, 6, 0, 0, 0, location),
		time.Date(2026, 10, 31, 8, 50, 0, 0, location),
	}
	for i, want := range wantDue {
		if !moments[i].DueAt.Equal(want) {
			t.Errorf("moments[%d].due_at = %v, want %v", i, moments[i].DueAt, want)
		}
		if moments[i].Kind != model.NotificationKindReminderDue {
			t.Errorf("moments[%d].kind = %q, want %q", i, moments[i].Kind, model.NotificationKindReminderDue)
		}
	}
	if want := "Напоминание «Обратный отсчёт» назначено на 09:00."; moments[0].Message != want {
		t.Errorf("message = %q, want %q", moments[0].Message, want)
	}
	if want := "Напоминание «Обратный отсчёт»: ждать ещё 3 ч."; moments[1].Message != want {
		t.Errorf("message = %q, want %q", moments[1].Message, want)
	}
}

func TestServiceSyncTaskStoresComputedMoment(t *testing.T) {
	store := &fakeScheduleStore{}
	service := NewService(store, &fakeTimezones{zone: "Europe/Moscow"}, time.Second, config.Reminder{}, testServiceLogs())

	task := taskOf("reminder", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), new("09:30"))
	if err := service.SyncTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	stored := store.moments[testTaskID]
	if len(stored) != 1 {
		t.Fatalf("stored moments = %d, want 1", len(stored))
	}
	if stored[0].Kind != model.NotificationKindReminderDue {
		t.Errorf("stored kind = %q", stored[0].Kind)
	}
}

func TestServiceSyncTaskSkipsMissingUser(t *testing.T) {
	store := &fakeScheduleStore{}
	service := NewService(store, &fakeTimezones{err: model.ErrUserNotFound}, time.Second, config.Reminder{}, testServiceLogs())

	task := taskOf("reminder", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), new("09:30"))
	if err := service.SyncTask(context.Background(), task); err != nil {
		t.Fatalf("missing user must be a no-op, got %v", err)
	}
	if len(store.moments[testTaskID]) != 0 {
		t.Errorf("unexpected stored moments")
	}
}

func TestServiceSyncTaskReturnsTimezoneFailure(t *testing.T) {
	store := &fakeScheduleStore{}
	service := NewService(store, &fakeTimezones{err: errors.New("redis down")}, time.Second, config.Reminder{}, testServiceLogs())

	task := taskOf("reminder", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), new("09:30"))
	if err := service.SyncTask(context.Background(), task); err == nil {
		t.Fatal("expected error")
	}
}

func TestServiceReconcileCoversTasksAndZone(t *testing.T) {
	store := &fakeScheduleStore{}
	tomorrow := time.Now().Add(24 * time.Hour).UTC()
	store.tasks = []model.TaskWithTimezone{
		{Task: taskOf("reminder", tomorrow, new("08:00")), Timezone: "UTC"},
	}
	service := NewService(store, &fakeTimezones{}, time.Second,
		config.Reminder{Horizon: 7 * 24 * time.Hour, ReconcileLimit: 100}, testServiceLogs())

	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.listLimit != 100 {
		t.Errorf("list limit = %d, want the configured one", store.listLimit)
	}
	if len(store.moments[testTaskID]) != 1 {
		t.Errorf("the upcoming task was not reconciled")
	}
}

func TestServiceReconcileKeepsGoingAfterOneFailure(t *testing.T) {
	store := &fakeScheduleStore{alwaysErr: true}
	tomorrow := time.Now().Add(24 * time.Hour).UTC()
	store.tasks = []model.TaskWithTimezone{
		{Task: taskOf("reminder", tomorrow, new("08:00")), Timezone: "UTC"},
	}
	// The healthy task must still go through even though one write failed.
	service := NewService(store, &fakeTimezones{}, time.Second, config.Reminder{ReconcileLimit: 10}, testServiceLogs())
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationMessageOfUndatedTask(t *testing.T) {
	if text := notificationMessage("task", "Черновик", time.Time{}); text == "" {
		t.Error("zero clock must still produce text")
	}
}

func TestSyncTaskSkipsWithoutStore(t *testing.T) {
	service := NewService(nil, &fakeTimezones{}, time.Second, config.Reminder{}, testServiceLogs())
	task := taskOf("reminder", time.Now().Add(time.Hour), new("09:00"))
	if err := service.SyncTask(context.Background(), task); err != nil {
		t.Fatalf("nil store must be a no-op, got %v", err)
	}
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatalf("nil store must be a no-op, got %v", err)
	}
}
