package reminder

import (
	"context"
	"errors"
	"fmt"
	"gorkycode/backend/internal/config"
	"gorkycode/backend/internal/lib/logger"
	"gorkycode/backend/internal/model"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// TimezoneReader supplies the timezone saved during registration.
type TimezoneReader interface {
	GetUserTimezone(ctx context.Context, userID uuid.UUID) (string, error)
}

// ScheduleStore persists reminder notifications per task.
type ScheduleStore interface {
	// ScheduleTask atomically replaces the future pending notifications of
	// one task with the supplied moments.
	ScheduleTask(ctx context.Context, userID, taskID uuid.UUID, moments []model.Notification) error
	// UpcomingTasksWithTimezone feeds the reconciliation scan.
	UpcomingTasksWithTimezone(ctx context.Context, from, to time.Time, limit int) ([]model.TaskWithTimezone, error)
}

// Syncer keeps reminder moments in step with task changes. It is wired into
// the task subservices as a fire-and-forget optimization: correctness does
// not depend on it because the scheduler reconciliation scan re-derives all
// upcoming moments from the tasks table on its own.
type Syncer interface {
	SyncTask(ctx context.Context, task model.Task) error
}

// Service computes reminder moments out of tasks and keeps the notifications
// table in step with them.
type Service struct {
	store        ScheduleStore
	timezones    TimezoneReader
	queryTimeout time.Duration
	log          *slog.Logger
	cfg          config.Reminder
	offsets      []time.Duration
	minLead      time.Duration
}

// NewService constructs the reminder service.
func NewService(store ScheduleStore, timezones TimezoneReader, queryTimeout time.Duration, cfg config.Reminder, log *slog.Logger) *Service {
	service := &Service{store: store, timezones: timezones, queryTimeout: queryTimeout, log: log, cfg: cfg, offsets: parseReminderOffsets(cfg.Offsets)}
	if service.cfg.MinAdvanceLead <= 0 {
		service.cfg.MinAdvanceLead = 5 * time.Hour
	}
	service.minLead = service.cfg.MinAdvanceLead
	setConfiguredOffsets(service.offsets)
	setConfiguredMinLead(service.minLead)
	return service
}

// SyncTask schedules notifications for one task. A title/time change simply
// re-schedules all future moments, deleting task rows removes its
// notifications through the database cascade.
func (s *Service) SyncTask(ctx context.Context, task model.Task) error {
	if s.store == nil {
		return nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()

	moments, err := s.moments(queryCtx, task)
	if err != nil {
		return err
	}
	return s.store.ScheduleTask(queryCtx, task.UserID, task.ID, moments)
}

// Reconcile re-derives the reminder moments of the active tasks inside the
// configured horizon. It heals everything the synchronous hooks could have
// missed: a skipped hook, a changed timezone, or a process restart.
func (s *Service) Reconcile(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	now := time.Now()
	from := now.AddDate(0, 0, -1)
	to := now.Add(s.cfg.Horizon)

	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout*4)
	defer cancel()
	tasks, err := s.store.UpcomingTasksWithTimezone(queryCtx, from, to, s.cfg.ReconcileLimit)
	if err != nil {
		return fmt.Errorf("list upcoming tasks: %w", err)
	}
	for _, row := range tasks {
		moments := upcomingMoments(row.Task, row.Timezone, now)
		if err = s.store.ScheduleTask(queryCtx, row.Task.UserID, row.Task.ID, moments); err != nil {
			s.log.ErrorContext(ctx, "reconcile task reminders failed",
				slog.String("task_id", row.Task.ID.String()),
				slog.String("user_id", row.Task.UserID.String()),
				logger.Err(err),
			)
			continue
		}
	}
	return nil
}

// moments reads the user timezone and supplies the notification records.
func (s *Service) moments(ctx context.Context, task model.Task) ([]model.Notification, error) {
	now := time.Now()
	tz, err := s.timezones.GetUserTimezone(ctx, task.UserID)
	if errors.Is(err, model.ErrUserNotFound) {
		// The user left while the request ran: nothing is scheduled.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read user timezone: %w", err)
	}

	moments := upcomingMoments(task, tz, now)
	if len(moments) == 0 {
		s.log.DebugContext(ctx, "no future reminder moments", slog.String("task_id", task.ID.String()))
	}
	return moments, nil
}

// upcomingMoments computes the future notification records of one task: its exact
// deadline moment and, for moments on later days, the advance reminders
// preceding it. A moment scheduled for today fires once: the user already
// pinned the exact minute, so same-day pings only add noise to short
// reminders like «напомни через час». Completed records, undated records or
// moments already past produce none.
func upcomingMoments(task model.Task, timezone string, now time.Time) []model.Notification {
	if task.Date == nil || task.Status != "active" || task.Title == "" {
		return nil
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		// As the contract requires, a missing timezone degrades to UTC.
		location = time.UTC
	}

	var kind string
	var baseMessage string
	switch task.Type {
	case "reminder":
		kind = model.NotificationKindReminderDue
	case "meeting":
		kind = model.NotificationKindMeetingStart
	case "task":
		kind = model.NotificationKindTaskDue
	default:
		return nil
	}
	baseMessage = notificationMessage(task.Type, task.Title, taskTime(task))

	var clock time.Time
	if task.Time != nil {
		clock, _ = time.Parse("15:04", *task.Time)
	}
	date := *task.Date
	hour, minute := clock.Hour(), clock.Minute()
	if clock.IsZero() {
		hour, minute = model.TaskDueByDefaultHour, 0
	}
	baseDue := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, location)

	var moments []model.Notification
	seen := map[time.Time]struct{}{}

	if baseDue.After(now) {
		seen[baseDue] = struct{}{}
		moments = append(moments, model.Notification{
			UserID:  task.UserID,
			TaskID:  task.ID,
			Kind:    kind,
			Title:   task.Title,
			Message: baseMessage,
			DueAt:   baseDue,
		})
	}

	if baseDue.Sub(now) > 3*time.Hour {
		for _, offset := range offsetsFor(task) {
			if offset <= 0 {
				continue
			}
			candidate := baseDue.Add(-offset)
			if !candidate.After(now) {
				continue
			}
			if candidate.Sub(now) < configuredMinLead {
				continue
			}
			if _, dup := seen[candidate]; dup {
				continue
			}
			seen[candidate] = struct{}{}
			moments = append(moments, model.Notification{
				UserID:  task.UserID,
				TaskID:  task.ID,
				Kind:    model.NotificationKindReminderDue,
				Title:   task.Title,
				Message: fmt.Sprintf("Напоминание «%s»: ждать ещё %s", task.Title, offsetLabel(offset)),
				DueAt:   candidate,
			})
		}
	}

	if len(moments) == 0 {
		return nil
	}
	return moments
}

// sameLocalDate reports whether both moments fall on one calendar day.
// Both arguments must already carry the same location.
func sameLocalDate(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

// offsetsFor returns the configured advance moments for this task. The
// service-level parser supplies them; an empty set means none are wanted.
func offsetsFor(_ model.Task) []time.Duration {
	// Service carries the parsed offsets; the package-level helper only
	// exists so upcomingMoments can be pure. Callers set it through the
	// package global below when running tests without the service.
	return configuredOffsets
}

var configuredOffsets []time.Duration //nolint:gochecknoglobals
var configuredMinLead = 5 * time.Hour //nolint:gochecknoglobals

func setConfiguredOffsets(values []time.Duration) {
	configuredOffsets = values
}

func setConfiguredMinLead(value time.Duration) {
	if value <= 0 {
		value = 5 * time.Hour
	}
	configuredMinLead = value
}

// taskTime ignores a missing clock safely for the message helpers.
func taskTime(task model.Task) time.Time {
	if task.Time != nil {
		value, err := time.Parse("15:04", *task.Time)
		if err == nil {
			return value
		}
	}
	return time.Time{}
}

func parseReminderOffsets(raw string) []time.Duration {
	var result []time.Duration
	seen := map[time.Duration]struct{}{}
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := time.ParseDuration(part)
		if err != nil || value <= 0 {
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}

	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j] > result[i] {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

func offsetLabel(value time.Duration) string {
	switch {
	case value >= 24*time.Hour && value%(24*time.Hour) == 0:
		return fmt.Sprintf("%d дн.", int(value/(24*time.Hour)))
	case value%time.Hour == 0:
		return fmt.Sprintf("%d ч.", int(value/time.Hour))
	case value%time.Minute == 0:
		return fmt.Sprintf("%d мин.", int(value/time.Minute))
	default:
		return value.String()
	}
}

// notificationMessage builds the human-readable notification text.
func notificationMessage(recordType, title string, clock time.Time) string {
	const dayLayout = "15:04"
	switch recordType {
	case "reminder":
		if clock.IsZero() {
			return fmt.Sprintf("Напоминание «%s» наступило.", title)
		}
		return fmt.Sprintf("Напоминание «%s» назначено на %s.", title, clock.Format(dayLayout))
	case "meeting":
		return fmt.Sprintf("Встреча «%s» начинается сейчас.", title)
	case "task":
		if clock.IsZero() {
			return fmt.Sprintf("Срок задачи «%s» наступает сегодня.", title)
		}
		return fmt.Sprintf("Срок задачи «%s» наступил.", title)
	default:
		return title
	}
}
