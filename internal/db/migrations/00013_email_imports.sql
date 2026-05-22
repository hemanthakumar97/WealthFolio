-- +goose Up
-- +goose StatementBegin
CREATE TABLE email_imports (
    id            BIGSERIAL PRIMARY KEY,
    message_id    TEXT        NOT NULL UNIQUE,
    thread_id     TEXT,
    sender        TEXT,
    subject       TEXT,
    received_at   TIMESTAMPTZ,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status        TEXT        NOT NULL DEFAULT 'OK'
                  CHECK (status IN ('OK','SKIPPED','ERROR')),
    upload_id     BIGINT REFERENCES upload_history(id) ON DELETE SET NULL,
    error_message TEXT
);
CREATE INDEX idx_email_imports_processed_at ON email_imports(processed_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS email_imports;
-- +goose StatementEnd
