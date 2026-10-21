-- +goose Up
-- +goose StatementBegin
-- tasks.type stores the same record_type values as the API and AI contracts.
ALTER TABLE tasks DROP CONSTRAINT tasks_type_check;

UPDATE tasks
SET type = CASE type
    WHEN 'встреча' THEN 'meeting'
    WHEN 'задача' THEN 'task'
    WHEN 'напоминание' THEN 'reminder'
END;

ALTER TABLE tasks
    ADD CONSTRAINT tasks_type_check CHECK (type IN ('meeting', 'task', 'reminder'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tasks DROP CONSTRAINT tasks_type_check;

UPDATE tasks
SET type = CASE type
    WHEN 'meeting' THEN 'встреча'
    WHEN 'task' THEN 'задача'
    WHEN 'reminder' THEN 'напоминание'
END;

ALTER TABLE tasks
    ADD CONSTRAINT tasks_type_check CHECK (type IN ('встреча', 'задача', 'напоминание'));
-- +goose StatementEnd
