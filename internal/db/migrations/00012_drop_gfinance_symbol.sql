-- +goose Up
DROP INDEX IF EXISTS idx_instruments_gfinance_symbol;
ALTER TABLE instruments DROP COLUMN IF EXISTS gfinance_symbol;

-- +goose Down
ALTER TABLE instruments ADD COLUMN gfinance_symbol TEXT;
CREATE INDEX idx_instruments_gfinance_symbol ON instruments(gfinance_symbol);
