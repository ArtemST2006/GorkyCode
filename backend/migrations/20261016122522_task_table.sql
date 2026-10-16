-- +goose Up
-- +goose StatementBegin
CREATE TABLE tasks (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type       TEXT NOT NULL CHECK (type IN ('встреча', 'задача', 'напоминание')),
    title      TEXT NOT NULL CHECK (btrim(title) <> ''),
    date       DATE,
    time       TIME,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    status     TEXT NOT NULL DEFAULT 'active'
               CHECK (status IN ('active', 'completed')),

    CHECK (time IS NULL OR date IS NOT NULL)
);

CREATE INDEX task_user_date_time_idx
    ON tasks (user_id, date, time);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tasks;
-- +goose StatementEnd
