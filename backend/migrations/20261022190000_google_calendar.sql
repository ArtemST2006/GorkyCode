-- +goose Up
-- +goose StatementBegin
-- One Google Calendar connection per user; tokens come from the OAuth callback.
CREATE TABLE google_integrations (
    user_id       UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    refresh_token TEXT NOT NULL CHECK (btrim(refresh_token) <> ''),
    access_token  TEXT NOT NULL DEFAULT '',
    token_expiry  TIMESTAMPTZ,
    calendar_id   TEXT NOT NULL DEFAULT 'primary' CHECK (btrim(calendar_id) <> ''),
    google_email  TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- NULL means the record has not been sent to Google Calendar.
ALTER TABLE tasks ADD COLUMN google_event_id TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tasks DROP COLUMN IF EXISTS google_event_id;
DROP TABLE IF EXISTS google_integrations;
-- +goose StatementEnd
