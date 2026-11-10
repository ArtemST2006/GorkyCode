-- +goose Up
-- +goose StatementBegin
-- Keep Google deletions after the local task row is gone. The task ID is not
-- a foreign key because the task is deleted in the same transaction.
ALTER TABLE google_integrations
    ADD COLUMN connection_id UUID NOT NULL DEFAULT gen_random_uuid();

CREATE TABLE google_event_deletions (
    task_id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    calendar_id TEXT NOT NULL,
    google_event_id TEXT NOT NULL,
    google_email TEXT,
    connection_id UUID NOT NULL,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX google_event_deletions_due_idx
    ON google_event_deletions (next_attempt_at, task_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE google_event_deletions;
ALTER TABLE google_integrations DROP COLUMN connection_id;
-- +goose StatementEnd
