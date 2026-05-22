-- +goose Up
-- +goose StatementBegin

CREATE TABLE prices (
    instrument_id BIGINT NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    price_date DATE NOT NULL,
    nav_price NUMERIC(18,6) NOT NULL,
    source TEXT NOT NULL DEFAULT 'GFINANCE',
    is_converted BOOLEAN NOT NULL DEFAULT FALSE,
    fund_name TEXT,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instrument_id, price_date)
);

SELECT create_hypertable('prices', 'price_date',
    chunk_time_interval => INTERVAL '30 days',
    if_not_exists => TRUE);

CREATE INDEX idx_prices_instrument_id ON prices(instrument_id, price_date DESC);

ALTER TABLE prices SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'instrument_id'
);
SELECT add_compression_policy('prices', INTERVAL '90 days', if_not_exists => TRUE);

-- market_cache: key-value store for scraped external data (MMI, metals, FX rates).
CREATE TABLE market_cache (
    cache_key TEXT PRIMARY KEY,
    cache_value JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS market_cache;
DROP TABLE IF EXISTS prices;
-- +goose StatementEnd
