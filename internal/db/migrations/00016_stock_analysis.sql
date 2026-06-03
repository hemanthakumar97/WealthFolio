-- +goose Up
-- +goose StatementBegin
CREATE TABLE stock_analysis (
    symbol           TEXT        NOT NULL,
    analysis_date    DATE        NOT NULL DEFAULT CURRENT_DATE,
    company_name     TEXT        NOT NULL DEFAULT '',
    sector           TEXT        NOT NULL DEFAULT '',
    -- Composite scores (0–100)
    composite_score    INT         NOT NULL DEFAULT 0,
    fundamental_score  INT         NOT NULL DEFAULT 0,
    technical_score    INT         NOT NULL DEFAULT 0,
    recommendation     TEXT        NOT NULL DEFAULT 'HOLD',
    -- Technical indicators
    rsi_14           NUMERIC(6,2)  NOT NULL DEFAULT 0,
    macd_histogram   NUMERIC(10,4) NOT NULL DEFAULT 0,
    sma_50           NUMERIC(14,2) NOT NULL DEFAULT 0,
    sma_200          NUMERIC(14,2) NOT NULL DEFAULT 0,
    current_price    NUMERIC(14,2) NOT NULL DEFAULT 0,
    above_sma_50     BOOLEAN       NOT NULL DEFAULT FALSE,
    above_sma_200    BOOLEAN       NOT NULL DEFAULT FALSE,
    golden_cross     BOOLEAN       NOT NULL DEFAULT FALSE,
    -- Fundamental snapshot
    trailing_pe      NUMERIC(8,2)  NOT NULL DEFAULT 0,
    price_to_book    NUMERIC(8,2)  NOT NULL DEFAULT 0,
    roe              NUMERIC(8,2)  NOT NULL DEFAULT 0,
    revenue_growth   NUMERIC(8,2)  NOT NULL DEFAULT 0,
    earnings_growth  NUMERIC(8,2)  NOT NULL DEFAULT 0,
    debt_to_equity   NUMERIC(8,2)  NOT NULL DEFAULT 0,
    dividend_yield   NUMERIC(8,2)  NOT NULL DEFAULT 0,
    market_cap_cr    NUMERIC(14,2) NOT NULL DEFAULT 0,
    week_52_high     NUMERIC(14,2) NOT NULL DEFAULT 0,
    week_52_low      NUMERIC(14,2) NOT NULL DEFAULT 0,
    -- Signal narratives (pipe-separated lists)
    buy_signals      TEXT          NOT NULL DEFAULT '',
    caution_signals  TEXT          NOT NULL DEFAULT '',
    -- Full metrics blob for future use
    metrics_json     JSONB,
    analyzed_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    PRIMARY KEY (symbol, analysis_date)
);
CREATE INDEX idx_stock_analysis_symbol ON stock_analysis(symbol, analysis_date DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS stock_analysis;
-- +goose StatementEnd
