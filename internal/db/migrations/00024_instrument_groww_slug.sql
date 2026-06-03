-- +goose Up
ALTER TABLE instruments ADD COLUMN IF NOT EXISTS groww_slug TEXT;

-- +goose Down
ALTER TABLE instruments DROP COLUMN IF EXISTS groww_slug;
