package postgres

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorkycode/backend/internal/model"
	"os"
	"testing"
	"time"
)

func TestConcurrentTaskChangesAreAtomic(t *testing.T) {
	url := os.Getenv("POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("set POSTGRES_TEST_URL")
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo := NewTaskRepository(pool)
	for _, operation := range []string{"update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			owner := createTaskTestUser(t, NewAuthRepository(pool), pool)
			ids := make([]uuid.UUID, 2)
			for i := range ids {
				if err := pool.QueryRow(ctx, `INSERT INTO tasks(user_id,type,title,date,time) VALUES($1,'meeting','Обсуждение','2026-10-27','15:00') RETURNING id`, owner).Scan(&ids[i]); err != nil {
					t.Fatal(err)
				}
			}
			shown, err := repo.FindCandidates(ctx, owner, "Обсуждение", nil, nil, nil, nil, nil)
			if err != nil || len(shown) != 2 {
				t.Fatalf("snapshot = %v, %v", shown, err)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() {
				<-start
				clock := "16:00"
				_, err := repo.UpdateTasks(ctx, owner, ids, shown, model.TaskChanges{Time: &clock}, nil)
				results <- err
			}()
			go func() {
				<-start
				reversed := []uuid.UUID{ids[1], ids[0]}
				var err error
				if operation == "delete" {
					_, err = repo.DeleteTasks(ctx, owner, reversed, shown)
				} else {
					clock := "17:00"
					_, err = repo.UpdateTasks(ctx, owner, reversed, shown, model.TaskChanges{Time: &clock}, nil)
				}
				results <- err
			}()
			close(start)
			first, second := <-results, <-results
			if !((first == nil && errors.Is(second, model.ErrStaleSelection)) || (second == nil && errors.Is(first, model.ErrStaleSelection))) {
				t.Fatalf("want one success and one conflict; got %v, %v", first, second)
			}
			var count, distinctTimes int
			if err := pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT time) FROM tasks WHERE user_id=$1`, owner).Scan(&count, &distinctTimes); err != nil {
				t.Fatal(err)
			}
			if count != 0 && (count != 2 || distinctTimes != 1) {
				t.Fatalf("partial batch: rows=%d, different times=%d", count, distinctTimes)
			}
		})
	}
}

func TestDeleteRejectsChangedSnapshotWithoutPartialDeletion(t *testing.T) {
	url := os.Getenv("POSTGRES_TEST_URL")
	if url == "" {
		t.Skip("set POSTGRES_TEST_URL")
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx := t.Context()
	owner := createTaskTestUser(t, NewAuthRepository(pool), pool)
	repo := NewTaskRepository(pool)
	for range 2 {
		if _, err := pool.Exec(ctx, `INSERT INTO tasks(user_id,type,title,date) VALUES($1,'task','Исходная запись','2026-10-27')`, owner); err != nil {
			t.Fatal(err)
		}
	}
	shown, err := repo.FindCandidates(ctx, owner, "", nil, nil, nil, nil, nil)
	if err != nil || len(shown) != 2 {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET title='Изменённая запись' WHERE id=$1`, shown[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DeleteTasks(ctx, owner, []uuid.UUID{shown[0].ID, shown[1].ID}, shown); !errors.Is(err, model.ErrStaleSelection) {
		t.Fatalf("stale deletion: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE user_id=$1`, owner).Scan(&count); err != nil || count != 2 {
		t.Fatalf("remaining=%d, %v", count, err)
	}
}
