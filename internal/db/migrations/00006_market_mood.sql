-- +goose Up
-- +goose StatementBegin

-- P/E, P/B, dividend-yield history per index (time-series).
CREATE TABLE market_data (
    index_name TEXT NOT NULL,
    price_date DATE NOT NULL,
    pe_ratio    NUMERIC,
    pb_ratio    NUMERIC,
    div_yield   NUMERIC,
    source      TEXT NOT NULL DEFAULT 'MANUAL',
    PRIMARY KEY (index_name, price_date)
);

SELECT create_hypertable('market_data', 'price_date',
    chunk_time_interval => INTERVAL '90 days',
    if_not_exists => TRUE);

ALTER TABLE market_data SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'index_name'
);
SELECT add_compression_policy('market_data', INTERVAL '180 days', if_not_exists => TRUE);

-- Rolling stats CAGG: daily average P/E per index (used for percentile mood).
CREATE MATERIALIZED VIEW cagg_index_pe_stats
WITH (timescaledb.continuous) AS
SELECT
    index_name,
    time_bucket('1 day', price_date) AS day,
    avg(pe_ratio) AS pe_ratio
FROM market_data
GROUP BY index_name, day
WITH NO DATA;

SELECT add_continuous_aggregate_policy('cagg_index_pe_stats',
    start_offset      => INTERVAL '5 years',
    end_offset        => INTERVAL '1 day',
    schedule_interval => INTERVAL '1 hour',
    if_not_exists     => TRUE);

-- Which indices are tracked and toggled on/off.
CREATE TABLE market_index_config (
    index_name   TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    category     TEXT NOT NULL DEFAULT 'Equity',
    is_active    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed the default Indian indices.
INSERT INTO market_index_config (index_name, display_name, category) VALUES
    ('NIFTY 50',     'NIFTY 50',         'Equity'),
    ('NIFTY 500',    'NIFTY 500',        'Equity'),
    ('SENSEX',       'BSE SENSEX',       'Equity'),
    ('NIFTY MIDCAP 100', 'NIFTY Midcap 100', 'Equity'),
    ('NIFTY SMALLCAP 100', 'NIFTY Smallcap 100', 'Equity')
ON CONFLICT DO NOTHING;

-- Watchlist: user-saved symbols to monitor (no FK to instruments — freeform).
CREATE TABLE watchlist (
    id         BIGSERIAL PRIMARY KEY,
    symbol     TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP MATERIALIZED VIEW IF EXISTS cagg_index_pe_stats;
DROP TABLE IF EXISTS watchlist;
DROP TABLE IF EXISTS market_index_config;
DROP TABLE IF EXISTS market_data;
-- +goose StatementEnd
