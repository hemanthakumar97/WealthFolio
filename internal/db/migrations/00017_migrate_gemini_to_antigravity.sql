-- +goose Up
-- +goose StatementBegin
UPDATE app_settings
SET value = 'antigravity'
WHERE key = 'ai_provider' AND value = 'gemini';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE app_settings
SET value = 'gemini'
WHERE key = 'ai_provider' AND value = 'antigravity';
-- +goose StatementEnd
