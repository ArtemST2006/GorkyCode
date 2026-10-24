package redis

import (
	"context"
	"errors"
	"fmt"
	"gorkycode/backend/internal/model"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// TaskProcess stores dialogue drafts in Redis.
type TaskProcess struct {
	client *goredis.Client
}

// NewTaskProcess creates a draft store over a Redis client.
func NewTaskProcess(client *goredis.Client) *TaskProcess {
	return &TaskProcess{
		client: client,
	}
}

// GetDraft returns the saved dialogue context.
// It returns model.ErrDraftNotFound when the draft does not exist.
func (p *TaskProcess) GetDraft(ctx context.Context, key string) (string, error) {
	value, err := p.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return "", model.ErrDraftNotFound
		}
		return "", fmt.Errorf("get draft: %w", err)
	}
	return value, nil
}

// PutDraft stores the dialogue context without expiration.
func (p *TaskProcess) PutDraft(ctx context.Context, key, value string) error {
	if err := p.client.Set(ctx, key, value, 0).Err(); err != nil {
		return fmt.Errorf("set draft: %w", err)
	}
	return nil
}

// SavePendingTask stores a task that waits for the user's confirmation.
func (p *TaskProcess) SavePendingTask(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := p.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("set pending task: %w", err)
	}
	return nil
}

// TakePendingTask returns the pending task and deletes it, so it is used once.
// It returns model.ErrPendingTaskNotFound when nothing waits for confirmation.
func (p *TaskProcess) TakePendingTask(ctx context.Context, key string) (string, error) {
	value, err := p.client.GetDel(ctx, key).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return "", model.ErrPendingTaskNotFound
		}
		return "", fmt.Errorf("take pending task: %w", err)
	}
	return value, nil
}
