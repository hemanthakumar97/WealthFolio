-- +goose Up
-- +goose StatementBegin
CREATE TABLE instrument_snapshots (
    snapshot_date  DATE    NOT NULL,
    instrument_id  BIGINT  NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    invested       NUMERIC(18,2) NOT NULL DEFAULT 0,
    value          NUMERIC(18,2) NOT NULL DEFAULT 0,
    profit         NUMERIC(18,2) NOT NULL DEFAULT 0,
    profit_percent NUMERIC(8,4)  NOT NULL DEFAULT 0,
    PRIMARY KEY (snapshot_date, instrument_id)
);
CREATE INDEX idx_instrument_snapshots_date       ON instrument_snapshots(snapshot_date);
CREATE INDEX idx_instrument_snapshots_instrument ON instrument_snapshots(instrument_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS instrument_snapshots;
-- +goose StatementEnd
