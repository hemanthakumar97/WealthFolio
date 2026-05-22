-- +goose Up
ALTER TABLE signal_results ADD COLUMN IF NOT EXISTS key_points    TEXT NOT NULL DEFAULT '';
ALTER TABLE signal_results ADD COLUMN IF NOT EXISTS qualitative_note TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE signal_results DROP COLUMN IF EXISTS key_points;
ALTER TABLE signal_results DROP COLUMN IF EXISTS qualitative_note;
