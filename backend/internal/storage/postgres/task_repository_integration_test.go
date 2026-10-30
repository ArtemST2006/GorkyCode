package postgres

import (
	"context"
	"encoding/json"
	"errors"
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

func TestDeleteTasksIsAtomicAndOwnerScoped(t *testing.T) {
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
	insert := func(userID uuid.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			"INSERT INTO tasks (user_id, type, title, date, time) VALUES ($1, 'task', 'atomic delete test', '2026-10-27', '14:00') RETURNING id",
			userID,
		).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, second, foreign := insert(owner), insert(owner), insert(stranger)
	repo := NewTaskRepository(pool)
	shown, err := repo.FindCandidates(ctx, owner, "", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DeleteTasks(ctx, owner, []uuid.UUID{first, foreign}, shown); !errors.Is(err, model.ErrStaleSelection) {
		t.Fatalf("mixed-owner delete error = %v; want ErrStaleSelection", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM tasks WHERE id = ANY($1)", []uuid.UUID{first, second, foreign}).Scan(&remaining); err != nil || remaining != 3 {
		t.Fatalf("rows after rollback = %d, %v; want 3", remaining, err)
	}
	deleted, err := repo.DeleteTasks(ctx, owner, []uuid.UUID{first, second}, shown)
	if err != nil || len(deleted) != 2 {
		t.Fatalf("owner delete result = %+v, %v; want two rows", deleted, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM tasks WHERE id = ANY($1)", []uuid.UUID{first, second, foreign}).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("rows after commit = %d, %v; want foreign row only", remaining, err)
	}
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

func TestCreateDateOnlyTaskIsVisibleToListEvents(t *testing.T) {
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
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	owner := createTaskTestUser(t, NewAuthRepository(pool), pool)
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	task := &model.Task{UserID: owner, Type: "task", Title: "Купить билеты", Date: &date}
	rep := NewTaskRepository(pool)
	if err := rep.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	from := date
	to := date.AddDate(0, 0, 1)
	got, err := rep.ListEvents(ctx, owner, from, to, nil)
	if err != nil || len(got) != 1 || got[0].ID != task.ID || got[0].Time != nil {
		t.Fatalf("listed date-only task = %+v, %v", got, err)
	}
	search, err := rep.SearchEvents(ctx, owner, "билеты", &from, &to, []string{"task"})
	if err != nil || len(search) != 1 || search[0].ID != task.ID || search[0].Time != nil {
		t.Fatalf("searched date-only task = %+v, %v", search, err)
	}
	vector := make([]float32, model.EmbeddingDimension)
	vector[0] = 1
	if err := rep.StoreEmbedding(ctx, task.ID, model.EmbeddingResult{
		Vector: vector, Model: "test/date-only", Version: "v1",
	}); err != nil {
		t.Fatal(err)
	}
	semantic, err := rep.FindSemantic(ctx, owner, vector, "test/date-only", "v1",
		&from, &to, nil, nil, []string{"task"})
	if err != nil || len(semantic) != 1 || semantic[0].ID != task.ID || semantic[0].Time != nil {
		t.Fatalf("semantic date-only task = %+v, %v", semantic, err)
	}
	timeFrom := "10:00"
	withoutTime, err := rep.FindCandidates(ctx, owner, "билеты", &from, &to,
		&timeFrom, nil, []string{"task"})
	if err != nil || len(withoutTime) != 0 {
		t.Fatalf("time-filtered date-only task = %+v, %v", withoutTime, err)
	}

	deleter := subservice.NewTaskDeleteService(rep, nil)
	find := json.RawMessage(`{"phase":"find_candidates","title_query":"билеты",` +
		`"date_from":"2026-11-01","date_to_exclusive":"2026-11-02",` +
		`"time_from":null,"time_to_exclusive":null,"record_types":["task"]}`)
	candidates, err := deleter.FindCandidates(ctx, owner, find)
	if err != nil || len(candidates.Stored) != 1 || candidates.Stored[0].Id != task.ID ||
		candidates.Stored[0].Time != nil || candidates.Items[0].Event.Time != nil {
		t.Fatalf("delete candidates = %+v, %v", candidates, err)
	}

	updater := subservice.NewTaskUpdateService(rep, deleter, nil)
	updateFind := json.RawMessage(`{"phase":"find_candidates","title_query":"билеты",` +
		`"date_from":"2026-11-01","date_to_exclusive":"2026-11-02",` +
		`"time_from":null,"time_to_exclusive":null,"record_types":["task"],` +
		`"changes":{"status":"completed"}}`)
	updateCandidates, err := updater.FindCandidates(ctx, owner, updateFind)
	if err != nil || len(updateCandidates.Stored) != 1 || updateCandidates.Stored[0].Time != nil {
		t.Fatalf("update candidates = %+v, %v", updateCandidates, err)
	}
	updated, err := updater.UpdateConfirmed(ctx, owner,
		json.RawMessage(`{"phase":"update_confirmed","record_ids":["`+task.ID.String()+`"]}`),
		updateCandidates.Snapshots, updateCandidates.Changes)
	if err != nil || len(updated) != 1 || updated[0].Status != "completed" || updated[0].Time != nil {
		t.Fatalf("updated date-only task = %+v, %v", updated, err)
	}

	// The earlier candidates describe active rows; refresh after the update.
	candidates, err = deleter.FindCandidates(ctx, owner, find)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := deleter.DeleteConfirmed(ctx, owner,
		json.RawMessage(`{"phase":"delete_confirmed","task_ids":["`+task.ID.String()+`"]}`),
		candidates.Stored)
	if err != nil || len(deleted) != 1 || deleted[0].Id != task.ID || deleted[0].Time != nil {
		t.Fatalf("deleted date-only task = %+v, %v", deleted, err)
	}
	remaining, err := rep.ListEvents(ctx, owner, from, to, nil)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining after delete = %+v, %v", remaining, err)
	}
}

func TestUpdateTasksChecksSnapshotsAndCommitsAtomically(t *testing.T) {
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
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	owner := createTaskTestUser(t, NewAuthRepository(pool), pool)
	other := createTaskTestUser(t, NewAuthRepository(pool), pool)
	repo := NewTaskRepository(pool)
	ids := make([]uuid.UUID, 0, 2)
	for _, entry := range []struct {
		user  uuid.UUID
		title string
	}{
		{owner, "Встреча с Иваном"},
		{owner, "Встреча с Петром"},
		{other, "Чужая встреча"},
	} {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO tasks (user_id, type, title, date, time)
			VALUES ($1, 'meeting', $2, '2026-10-25', '15:00') RETURNING id`,
			entry.user, entry.title).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if entry.user == owner {
			ids = append(ids, id)
		}
	}
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	shown, err := repo.ListEvents(ctx, owner, from, from.AddDate(0, 0, 1), nil)
	if err != nil || len(shown) != 2 {
		t.Fatalf("shown = %+v, %v", shown, err)
	}
	newDate := from.AddDate(0, 0, 1)
	newTime := "16:00"
	changes := model.TaskChanges{Date: &newDate, Time: &newTime}
	updated, err := repo.UpdateTasks(ctx, owner, ids, shown, changes, nil)
	if err != nil || len(updated) != 2 {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	for _, task := range updated {
		if task.Date == nil || task.Date.Format(time.DateOnly) != "2026-10-26" || task.Time == nil || *task.Time != "16:00" {
			t.Fatalf("updated row = %+v", task)
		}
	}
	var otherTime string
	if err := pool.QueryRow(ctx, `SELECT to_char(time, 'HH24:MI') FROM tasks WHERE user_id = $1`, other).Scan(&otherTime); err != nil || otherTime != "15:00" {
		t.Fatalf("other user's time = %q, %v", otherTime, err)
	}
	newTitle := "Встреча перенесена"
	vector := make([]float32, model.EmbeddingDimension)
	vector[0] = 1
	titleUpdated, err := repo.UpdateTasks(ctx, owner, ids, updated,
		model.TaskChanges{Title: &newTitle},
		&model.EmbeddingResult{Vector: vector, Model: "integration-test", Version: "1"})
	if err != nil || len(titleUpdated) != 2 {
		t.Fatalf("title update = %+v, %v", titleUpdated, err)
	}
	for _, task := range titleUpdated {
		if task.Title != newTitle {
			t.Fatalf("updated title = %q", task.Title)
		}
	}
	var embeddings int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM task_embeddings e
		JOIN tasks t ON t.id = e.task_id WHERE t.user_id = $1`, owner).Scan(&embeddings); err != nil || embeddings != 2 {
		t.Fatalf("updated embeddings = %d, %v", embeddings, err)
	}
	// A stale choice must roll the whole batch back, including the first ID.
	newTime = "17:00"
	_, err = repo.UpdateTasks(ctx, owner, ids, shown, model.TaskChanges{Time: &newTime}, nil)
	if !errors.Is(err, model.ErrStaleSelection) {
		t.Fatalf("stale selection error = %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE user_id = $1 AND time = '16:00'`, owner).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rows kept after failed batch = %d, %v", count, err)
	}
}

func TestTaskProgressStatusIsRestrictedToTasksWithPostgres(t *testing.T) {
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
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	owner := createTaskTestUser(t, NewAuthRepository(pool), pool)
	repo := NewTaskRepository(pool)
	var taskID, meetingID uuid.UUID
	for _, entry := range []struct {
		typ string
		id  *uuid.UUID
	}{
		{"task", &taskID}, {"meeting", &meetingID},
	} {
		if err := pool.QueryRow(ctx, `INSERT INTO tasks (user_id, type, title, date, status)
			VALUES ($1, $2, 'Статус', '2026-10-30', 'active') RETURNING id`,
			owner, entry.typ).Scan(entry.id); err != nil {
			t.Fatal(err)
		}
	}
	date := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	shown, err := repo.ListEvents(ctx, owner, date, date.AddDate(0, 0, 1), nil)
	if err != nil || len(shown) != 2 {
		t.Fatalf("shown=%+v, error=%v", shown, err)
	}
	progress := "in_progress"
	updated, err := repo.UpdateTasks(ctx, owner, []uuid.UUID{taskID}, shown, model.TaskChanges{Status: &progress}, nil)
	if err != nil || len(updated) != 1 || updated[0].Status != progress {
		t.Fatalf("task progress update=%+v, error=%v", updated, err)
	}
	_, err = repo.UpdateTasks(ctx, owner, []uuid.UUID{meetingID}, shown, model.TaskChanges{Status: &progress}, nil)
	if !errors.Is(err, model.ErrInvalidAction) {
		t.Fatalf("meeting update error=%v, want invalid action", err)
	}
	for _, typ := range []string{"meeting", "reminder"} {
		_, err := pool.Exec(ctx, `INSERT INTO tasks (user_id, type, title, date, status)
			VALUES ($1, $2, 'Нельзя', '2026-10-30', 'in_progress')`, owner, typ)
		if err == nil {
			t.Fatalf("database accepted in_progress for %s", typ)
		}
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

func TestUpdateMeetingTimeOnlyPreservesOtherFields(t *testing.T) {
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
	owner := createTaskTestUser(t, NewAuthRepository(pool), pool)
	for _, date := range []string{"2026-10-28", "2026-10-29"} {
		_, err := pool.Exec(ctx, `INSERT INTO tasks (user_id, type, title, date, time)
            VALUES ($1, 'meeting', 'Встреча с Таней', $2::date, '15:00')`, owner, date)
		if err != nil {
			t.Fatal(err)
		}
	}
	repo := NewTaskRepository(pool)
	updater := subservice.NewTaskUpdateService(repo, subservice.NewTaskDeleteService(repo, nil), nil)
	candidates, err := updater.FindCandidates(ctx, owner, json.RawMessage(`{
        "phase":"find_candidates", "title_query":"с Таней", "record_types":["meeting"],
        "date_from":null, "date_to_exclusive":null, "time_from":null, "time_to_exclusive":null,
        "changes":{"time":"19:00"}
    }`))
	if err != nil || len(candidates.Items) != 2 {
		t.Fatalf("candidates=%+v, err=%v", candidates, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE user_id=$1 AND time='15:00'`, owner).Scan(&count); err != nil || count != 2 {
		t.Fatalf("search modified rows: count=%d, err=%v", count, err)
	}
	selected := candidates.Snapshots[0]
	updated, err := updater.UpdateConfirmed(ctx, owner,
		json.RawMessage(`{"phase":"update_confirmed","record_ids":["`+selected.ID.String()+`"]}`),
		candidates.Snapshots, candidates.Changes)
	if err != nil || len(updated) != 1 {
		t.Fatalf("updated=%+v, err=%v", updated, err)
	}
	item := updated[0]
	if item.Time == nil || string(*item.Time) != "19:00" || item.Date == nil ||
		item.Date.Format(time.DateOnly) != selected.Date.Format(time.DateOnly) ||
		item.Title != selected.Title || string(item.Status) != selected.Status ||
		item.Id != selected.ID || item.UserId != selected.UserID || string(item.RecordType) != selected.Type {
		t.Fatalf("time-only update changed other fields: before=%+v after=%+v", selected, item)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE user_id=$1 AND time='15:00'`, owner).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unselected row changed: count=%d, err=%v", count, err)
	}
}
