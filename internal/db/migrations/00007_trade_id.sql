-- +goose Up
-- +goose StatementBegin

ALTER TABLE transactions ADD COLUMN trade_id TEXT;

CREATE UNIQUE INDEX idx_transactions_trade_id
    ON transactions(instrument_id, trade_id, transaction_type)
    WHERE trade_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_transactions_trade_id;
ALTER TABLE transactions DROP COLUMN IF EXISTS trade_id;
-- +goose StatementEnd
