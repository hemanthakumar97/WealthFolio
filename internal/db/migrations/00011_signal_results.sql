-- +goose Up
-- +goose StatementBegin
CREATE TABLE signal_results (
    instrument_id    BIGINT      NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    risk_profile     TEXT        NOT NULL CHECK (risk_profile IN ('conservative','moderate','aggressive')),
    action           TEXT        NOT NULL,
    confidence       INT         NOT NULL DEFAULT 70,
    reason           TEXT        NOT NULL DEFAULT '',
    tax_note         TEXT        NOT NULL DEFAULT '',
    zero1_score      INT         NOT NULL DEFAULT 0,
    key_points       TEXT        NOT NULL DEFAULT '',
    qualitative_note TEXT        NOT NULL DEFAULT '',
    generated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instrument_id, risk_profile)
);

CREATE INDEX idx_signal_results_risk_profile ON signal_results(risk_profile, generated_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS signal_results;
-- +goose StatementEnd
