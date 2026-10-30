-- +goose Up
-- +goose StatementBegin
CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('task_due', 'reminder_due', 'meeting_start')),
    title TEXT NOT NULL CHECK (btrim(title) <> ''),
    message TEXT NOT NULL CHECK (btrim(message) <> ''),
    due_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered')),
    send_attempted_at TIMESTAMPTZ,
    email_sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE notifications IS 'Sheduled and delivered reminder notifications.';

-- One notification of a kind per task moment, as the WebSocket contract requires.
CREATE UNIQUE INDEX notifications_task_moment_kind_idx ON notifications (task_id, kind, due_at);

-- The scheduler claims due rows and the hub resends unacked ones on reconnect.
CREATE INDEX notifications_pending_due_idx ON notifications (due_at) WHERE status = 'pending';

-- The hub lists a user's unacked notifications by creation order.
CREATE INDEX notifications_user_pending_idx ON notifications (user_id, created_at) WHERE status = 'pending';

-- The retention sweep removes old records.
CREATE INDEX notifications_retention_idx ON notifications (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE notifications;
-- +goose StatementEnd
