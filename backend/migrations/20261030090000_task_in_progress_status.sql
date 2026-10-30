-- +goose Up
-- +goose StatementBegin
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check
    CHECK (status IN ('active', 'completed') OR (type = 'task' AND status = 'in_progress'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE tasks SET status = 'active' WHERE type = 'task' AND status = 'in_progress';
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check CHECK (status IN ('active', 'completed'));
-- +goose StatementEnd
