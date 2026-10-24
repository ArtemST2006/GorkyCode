package postgres

import (
	"context"
	"gorkycode/backend/internal/model"
	"gorkycode/backend/internal/service/subservice"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TaskRepository must satisfy the storage interface of the list_events service.
var _ subservice.TaskLister = (*TaskRepository)(nil)

func TestTaskRepositoryListEventsWithPostgres(t *testing.T) {
	url := os.Getenv("POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("set POSTGRES_TEST_URL for a migrated test database")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	auth := NewAuthRepository(pool)
	owner := createTaskTestUser(t, auth, pool)
	stranger := createTaskTestUser(t, auth, pool)

	// The period is [2026-10-20, 2026-10-22).
	inserts := []struct {
		userID uuid.UUID
		typ    string
		title  string
		date   any
		time   any
		status string
	}{
		{owner, "meeting", "before period", "2026-10-19", "10:00", "active"},
		{owner, "task", "first day without time", "2026-10-20", nil, "completed"},
		{owner, "meeting", "first day afternoon", "2026-10-20", "15:30", "active"},
		{owner, "reminder", "first day morning", "2026-10-20", "09:05", "active"},
		{owner, "task", "last day", "2026-10-21", "23:59", "active"},
		{owner, "meeting", "period end", "2026-10-22", "00:00", "active"},
		{owner, "task", "undated", nil, nil, "active"},
		{stranger, "meeting", "stranger inside period", "2026-10-20", "12:00", "active"},
	}
	for _, task := range inserts {
		if _, err = pool.Exec(ctx,
			"INSERT INTO tasks (user_id, type, title, date, time, status) VALUES ($1, $2, $3, $4::date, $5::time, $6)",
			task.userID, task.typ, task.title, task.date, task.time, task.status,
		); err != nil {
			t.Fatal(err)
		}
	}

	repo := NewTaskRepository(pool)
	from := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)

	t.Run("all types in period", func(t *testing.T) {
		got, err := repo.ListEvents(ctx, owner, from, to, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"first day without time", "first day morning", "first day afternoon", "last day"}
		if titles := taskTitles(got); !slices.Equal(titles, want) {
			t.Fatalf("titles = %v, want %v", titles, want)
		}

		withoutTime, morning := got[0], got[1]
		if withoutTime.Time != nil || withoutTime.Status != "completed" || withoutTime.Type != "task" {
			t.Fatalf("task without time = %+v", withoutTime)
		}
		if morning.Time == nil || *morning.Time != "09:05" {
			t.Fatalf("morning time = %v, want 09:05", morning.Time)
		}
		if morning.Date == nil || morning.Date.Format(time.DateOnly) != "2026-10-20" {
			t.Fatalf("morning date = %v, want 2026-10-20", morning.Date)
		}
		if morning.UserID != owner || morning.ID == uuid.Nil || morning.CreatedAt.IsZero() {
			t.Fatalf("morning task = %+v", morning)
		}
	})

	t.Run("type filter", func(t *testing.T) {
		got, err := repo.ListEvents(ctx, owner, from, to, []string{"meeting", "reminder"})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"first day morning", "first day afternoon"}
		if titles := taskTitles(got); !slices.Equal(titles, want) {
			t.Fatalf("titles = %v, want %v", titles, want)
		}
	})

	t.Run("other user", func(t *testing.T) {
		got, err := repo.ListEvents(ctx, stranger, from, to, nil)
		if err != nil {
			t.Fatal(err)
		}
		if titles := taskTitles(got); !slices.Equal(titles, []string{"stranger inside period"}) {
			t.Fatalf("titles = %v, want only the stranger's task", titles)
		}
	})

	t.Run("no tasks", func(t *testing.T) {
		got, err := repo.ListEvents(ctx, uuid.New(), from, to, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("ListEvents() = %#v, want empty non-nil slice", got)
		}
	})
}

func TestTaskRepositorySearchEventsWithPostgres(t *testing.T) {
	url := os.Getenv("POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("set POSTGRES_TEST_URL for a migrated test database")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	owner := createTaskTestUser(t, NewAuthRepository(pool), pool)
	stranger := createTaskTestUser(t, NewAuthRepository(pool), pool)
	for _, task := range []struct {
		userID     uuid.UUID
		typ, title string
		date       any
	}{
		{owner, "meeting", "Meeting with IVAN", "2026-10-20"},
		{owner, "task", "100% done", nil},
		{owner, "task", "Call Ivan", "2026-10-21"},
		{stranger, "meeting", "Meeting with Ivan", "2026-10-20"},
	} {
		if _, err = pool.Exec(ctx, "INSERT INTO tasks (user_id, type, title, date) VALUES ($1, $2, $3, $4::date)", task.userID, task.typ, task.title, task.date); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewTaskRepository(pool)
	from := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	got, err := repo.SearchEvents(ctx, owner, "ivan", &from, &to, []string{"meeting"})
	if err != nil || !slices.Equal(taskTitles(got), []string{"Meeting with IVAN"}) {
		t.Fatalf("filtered search = %v, %v", taskTitles(got), err)
	}
	got, err = repo.SearchEvents(ctx, owner, "%", nil, nil, nil)
	if err != nil || !slices.Equal(taskTitles(got), []string{"100% done"}) {
		t.Fatalf("literal wildcard search = %v, %v", taskTitles(got), err)
	}
}

func TestCreateTaskIsVisibleToListEvents(t *testing.T) {
	url := os.Getenv("POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("set POSTGRES_TEST_URL for a migrated test database")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	owner := createTaskTestUser(t, NewAuthRepository(pool), pool)
	location, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, 10, 21, 0, 0, 0, 0, location)
	clock := "15:00"
	task := &model.Task{UserID: owner, Type: "meeting", Title: "Встреча с Иваном", Date: &date, Time: &clock}
	if err = NewTaskRepository(pool).CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if task.ID == uuid.Nil || task.Status != "active" || task.CreatedAt.IsZero() {
		t.Fatalf("created task = %+v", task)
	}
	from := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	got, err := NewTaskRepository(pool).ListEvents(ctx, owner, from, to, nil)
	if err != nil || len(got) != 1 || got[0].ID != task.ID || got[0].Date == nil || got[0].Date.Format(time.DateOnly) != "2026-10-21" || got[0].Time == nil || *got[0].Time != clock {
		t.Fatalf("listed created task = %+v, %v", got, err)
	}
}

func createTaskTestUser(t *testing.T, auth *AuthRepository, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	user := &model.User{Name: "Task test", Email: uuid.NewString() + "@example.com", PasswordHash: "test hash", Timezone: "Europe/Moscow"}
	if err := auth.CreateUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	// Tasks are removed by ON DELETE CASCADE.
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", user.ID) })
	return user.ID
}

func taskTitles(tasks []model.Task) []string {
	titles := make([]string, 0, len(tasks))
	for _, task := range tasks {
		titles = append(titles, task.Title)
	}
	return titles
}

func TestTaskRepositoryFindOverlappingMeetingsWithPostgres(t *testing.T) {
	url := os.Getenv("POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("set POSTGRES_TEST_URL for a migrated test database")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	auth := NewAuthRepository(pool)
	owner := createTaskTestUser(t, auth, pool)
	stranger := createTaskTestUser(t, auth, pool)
	for _, task := range []struct {
		userID uuid.UUID
		typ    string
		title  string
		date   any
		time   any
		status string
	}{
		{owner, "meeting", "ends at new start", "2026-10-21", "14:00", "active"},
		{owner, "meeting", "starts inside", "2026-10-21", "15:30", "active"},
		{owner, "meeting", "starts at new end", "2026-10-21", "16:00", "active"},
		{owner, "meeting", "completed inside", "2026-10-21", "15:00", "completed"},
		{owner, "reminder", "reminder inside", "2026-10-21", "15:00", "active"},
		{owner, "meeting", "other day", "2026-10-22", "15:00", "active"},
		{owner, "meeting", "starts before and overlaps", "2026-10-21", "14:30", "active"},
		{stranger, "meeting", "stranger inside", "2026-10-21", "15:00", "active"},
	} {
		if _, err = pool.Exec(ctx,
			"INSERT INTO tasks (user_id, type, title, date, time, status) VALUES ($1, $2, $3, $4::date, $5::time, $6)",
			task.userID, task.typ, task.title, task.date, task.time, task.status,
		); err != nil {
			t.Fatal(err)
		}
	}

	repo := NewTaskRepository(pool)
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 21, 15, 0, 0, 0, moscow)

	got, err := repo.FindOverlappingMeetings(ctx, owner, start, start.Add(time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"starts before and overlaps", "starts inside"}
	if titles := taskTitles(got); !slices.Equal(titles, want) {
		t.Fatalf("titles = %v, want %v", titles, want)
	}

	free := time.Date(2026, 10, 21, 18, 0, 0, 0, moscow)
	if got, err = repo.FindOverlappingMeetings(ctx, owner, free, free.Add(time.Hour), time.Hour); err != nil || len(got) != 0 {
		t.Fatalf("FindOverlappingMeetings() in a free slot = %v, %v; want none", taskTitles(got), err)
	}
}
