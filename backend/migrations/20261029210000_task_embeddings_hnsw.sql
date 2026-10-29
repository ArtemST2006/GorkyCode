-- +goose NO TRANSACTION
-- +goose Up
-- Defaults from pgvector; query tuning is deliberately separate.
CREATE INDEX CONCURRENTLY task_embeddings_embedding_hnsw_idx
    ON task_embeddings USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- +goose Down
DROP INDEX CONCURRENTLY task_embeddings_embedding_hnsw_idx;
