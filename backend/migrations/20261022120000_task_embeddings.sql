-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE task_embeddings (
    task_id       UUID PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    embedding     vector(384) NOT NULL CHECK (vector_norm(embedding) > 0),
    model         TEXT NOT NULL CHECK (btrim(model) <> ''),
    model_version TEXT NOT NULL CHECK (btrim(model_version) <> ''),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE task_embeddings;
-- Keep the vector extension: it may be used by other database objects.
-- +goose StatementEnd
