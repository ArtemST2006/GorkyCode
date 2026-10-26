package redis

import (
	"context"
	"errors"
	"fmt"
	"gorkycode/backend/internal/model"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// Drafts are temporary dialogue state, so abandoned conversations expire.
const taskDraftTTL = 24 * time.Hour

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

// PutDraft stores the dialogue context for a bounded period. A reply renews it.
func (p *TaskProcess) PutDraft(ctx context.Context, key, value string) error {
	if err := p.client.Set(ctx, key, value, taskDraftTTL).Err(); err != nil {
		return fmt.Errorf("set draft: %w", err)
	}
	return nil
}

// PutDrafts publishes the dialogue and its candidate snapshot atomically.
func (p *TaskProcess) PutDrafts(ctx context.Context, values map[string]string) error {
	_, err := p.client.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		for key, value := range values {
			pipe.Set(ctx, key, value, taskDraftTTL)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("save dialogue state: %w", err)
	}
	return nil
}

// LockDialogue excludes concurrent turns across backend replicas. The caller
// must finish or cancel all work before ttl. A token prevents an old request
// from releasing a newer lease after a crash/expiry.
func (p *TaskProcess) LockDialogue(ctx context.Context, key string, ttl time.Duration) (func(), error) {
	token := uuid.NewString()
	locked, err := p.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: lock dialogue: %w", model.ErrServiceUnavailable, err)
	}
	if !locked {
		return nil, model.ErrDialogueBusy
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = p.client.Eval(releaseCtx, `if redis.call('GET', KEYS[1]) == ARGV[1] then
			return redis.call('DEL', KEYS[1]) end; return 0`, []string{key}, token).Err()
	}, nil
}

// DeleteDraft removes completed dialogue state. Removing an absent key is safe.
func (p *TaskProcess) SavePendingTask(ctx context.Context, key, value string, ttl time.Duration) error {
	return p.client.Set(ctx, key, value, ttl).Err()
}

func (p *TaskProcess) TakePendingTask(ctx context.Context, key string) (string, error) {
	value, err := p.client.GetDel(ctx, key).Result()
	if errors.Is(err, goredis.Nil) {
		return "", model.ErrPendingTaskNotFound
	}
	return value, err
}

func (p *TaskProcess) DeleteDraft(ctx context.Context, key string) error {
	if err := p.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("delete draft: %w", err)
	}
	return nil
}
