-- +goose Up
-- enable pg_trgm for fuzzy MF name matching in instrument FindOrCreate
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- +goose Down
DROP EXTENSION IF EXISTS pg_trgm;
