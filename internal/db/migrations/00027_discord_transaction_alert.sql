-- +goose Up
-- +goose StatementBegin
INSERT INTO app_settings (key, value)
VALUES ('discord_transaction_alert_enabled', 'true')
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM app_settings WHERE key = 'discord_transaction_alert_enabled';
-- +goose StatementEnd
