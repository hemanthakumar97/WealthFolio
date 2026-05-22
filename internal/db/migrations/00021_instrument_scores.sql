-- +goose Up
CREATE TABLE IF NOT EXISTS instrument_scores (
    instrument_id BIGINT PRIMARY KEY REFERENCES instruments(id) ON DELETE CASCADE,
    zero1_score   INT         NOT NULL DEFAULT 0,
    kind          TEXT        NOT NULL, -- 'mf' | 'etf' | 'stock'
    metrics_json  JSONB       NOT NULL DEFAULT '{}',
    computed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS instrument_scores;
