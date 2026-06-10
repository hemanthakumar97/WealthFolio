-- +goose Up
-- +goose StatementBegin
CREATE TABLE fx_rates (
    rate_date   DATE PRIMARY KEY,
    usd_to_inr  NUMERIC(18,6) NOT NULL,
    source      TEXT NOT NULL DEFAULT 'frankfurter',
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS fx_rates;
-- +goose StatementEnd
