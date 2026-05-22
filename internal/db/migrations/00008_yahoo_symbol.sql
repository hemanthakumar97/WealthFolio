-- +goose Up
-- +goose StatementBegin
ALTER TABLE instruments ADD COLUMN yahoo_symbol TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE instruments DROP COLUMN IF EXISTS yahoo_symbol;
-- +goose StatementEnd
