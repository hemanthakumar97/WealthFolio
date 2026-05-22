-- +goose Up
-- +goose StatementBegin

CREATE TABLE portfolio_snapshots (
    snapshot_date DATE NOT NULL,
    total_invested NUMERIC(18,2) NOT NULL DEFAULT 0,
    total_value NUMERIC(18,2) NOT NULL DEFAULT 0,
    total_profit NUMERIC(18,2) NOT NULL DEFAULT 0,
    profit_percentage NUMERIC(8,4) NOT NULL DEFAULT 0,
    snapshot_details JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (snapshot_date)
);

SELECT create_hypertable('portfolio_snapshots', 'snapshot_date',
    chunk_time_interval => INTERVAL '90 days',
    if_not_exists => TRUE);

ALTER TABLE portfolio_snapshots SET (timescaledb.compress);
SELECT add_compression_policy('portfolio_snapshots', INTERVAL '180 days', if_not_exists => TRUE);

-- Monthly returns continuous aggregate (refresh hourly, lag 1 day so today's partial day is excluded).
CREATE MATERIALIZED VIEW cagg_monthly_returns
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 month', snapshot_date) AS month,
    last(total_invested,    snapshot_date)::float AS invested,
    last(total_value,       snapshot_date)::float AS value,
    last(total_profit,      snapshot_date)::float AS profit,
    last(profit_percentage, snapshot_date)::float AS profit_percent
FROM portfolio_snapshots
GROUP BY month
WITH NO DATA;

SELECT add_continuous_aggregate_policy('cagg_monthly_returns',
    start_offset => INTERVAL '3 months',
    end_offset   => INTERVAL '1 day',
    schedule_interval => INTERVAL '1 hour',
    if_not_exists => TRUE);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP MATERIALIZED VIEW IF EXISTS cagg_monthly_returns;
DROP TABLE IF EXISTS portfolio_snapshots;
-- +goose StatementEnd
