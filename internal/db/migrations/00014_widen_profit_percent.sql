-- +goose Up
ALTER TABLE instrument_snapshots
  ALTER COLUMN profit_percent TYPE numeric(12,4);

-- +goose Down
ALTER TABLE instrument_snapshots
  ALTER COLUMN profit_percent TYPE numeric(8,4);
