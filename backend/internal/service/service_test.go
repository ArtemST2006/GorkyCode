package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeHealthRepository struct {
	called bool
	err    error
}

func (r *fakeHealthRepository) Ping(ctx context.Context) error {
	r.called = true
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("missing query deadline")
	}
	return r.err
}

func TestReadyUsesRepositoryWithTimeout(t *testing.T) {
	repo := &fakeHealthRepository{}
	services := NewHealthService(repo, time.Second)
	if err := services.Ready(t.Context()); err != nil {
		t.Fatalf("Ready() error: %v", err)
	}
	if !repo.called {
		t.Fatal("Ready() did not call repository")
	}
}
