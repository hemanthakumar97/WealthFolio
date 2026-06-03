-- +goose Up
ALTER TABLE instruments ADD COLUMN IF NOT EXISTS tickertape_slug text;

-- +goose Down
ALTER TABLE instruments DROP COLUMN IF EXISTS tickertape_slug;
