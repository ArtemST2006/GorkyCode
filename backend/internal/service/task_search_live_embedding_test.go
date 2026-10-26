package service

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorkycode/backend/internal/model"
	"gorkycode/backend/internal/service/subservice"
	"gorkycode/backend/internal/storage/postgres"
	"os"
	"testing"
)

// Opt-in integration test against the local embedding encoder; no LLM calls.
func TestSearchWithLocalEmbeddingsAndPostgres(t *testing.T) {
	url, embedURL := os.Getenv("POSTGRES_TEST_URL"), os.Getenv("LOCAL_EMBED_TEST_URL")
	if url == "" || embedURL == "" {
		t.Skip("set POSTGRES_TEST_URL and LOCAL_EMBED_TEST_URL (local encoder only)")
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	auth := postgres.NewAuthRepository(pool)
	newOwner := func() uuid.UUID {
		user := &model.User{Name: "Review", Email: uuid.NewString() + "@example.com", PasswordHash: "test", Timezone: "Europe/Moscow"}
		if err := auth.CreateUser(t.Context(), user); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", user.ID) })
		return user.ID
	}
	owner, other := newOwner(), newOwner()
	repo := postgres.NewTaskRepository(pool)
	embedder := NewAIEmbedder(embedURL)
	titles := []string{"Встреча с Иваном", "Встреча с Таней", "Встреча с Петей", "Приём у стоматолога", "Купить билеты", "Подготовить презентацию"}
	for _, title := range titles {
		vector, err := embedder.Embed(t.Context(), "document", title)
		if err != nil {
			t.Fatal(err)
		}
		for _, user := range []uuid.UUID{owner, other} {
			var id uuid.UUID
			if err := pool.QueryRow(t.Context(), `INSERT INTO tasks(user_id,type,title,date,time) VALUES($1,'meeting',$2,'2026-10-27','15:00') RETURNING id`, user, title).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if err := repo.StoreEmbedding(t.Context(), id, vector); err != nil {
				t.Fatal(err)
			}
		}
	}
	lister := subservice.NewSemanticLister(repo, repo, embedder)
	for _, tc := range []struct{ query, title string }{
		{"с Иваном", "Встреча с Иваном"}, {"с иваном", "Встреча с Иваном"},
		{"с Таней", "Встреча с Таней"}, {"с таней", "Встреча с Таней"},
		{"зубной врач", "Приём у стоматолога"}, {"про зубы", "Приём у стоматолога"},
		{"приобрести проездные документы", "Купить билеты"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			action, _ := json.Marshal(map[string]any{"query": tc.query})
			got, err := lister.SearchEvents(t.Context(), owner, action)
			if err != nil || len(got.Items) != 1 || got.Items[0].Title != tc.title || got.Items[0].UserId != owner {
				t.Fatalf("search %q = %+v, %v", tc.query, got, err)
			}
		})
	}
	// A different person's meeting must not replace a missing name match.
	if _, err := pool.Exec(t.Context(), `DELETE FROM tasks WHERE user_id=$1 AND title='Встреча с Иваном'`, owner); err != nil {
		t.Fatal(err)
	}
	got, err := lister.SearchEvents(t.Context(), owner, json.RawMessage(`{"query":"с иваном"}`))
	if err != nil || len(got.Items) != 0 {
		t.Fatalf("absent person = %+v, %v", got, err)
	}
}
