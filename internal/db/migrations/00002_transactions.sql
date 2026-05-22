-- +goose Up
-- +goose StatementBegin

CREATE TABLE instruments (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    isin TEXT UNIQUE,
    amfi_code TEXT,
    gfinance_symbol TEXT,
    asset_type TEXT NOT NULL CHECK (asset_type IN ('MF','ETF','STOCK','BOND','GOLD','OTHER')),
    currency TEXT NOT NULL DEFAULT 'INR' CHECK (currency IN ('INR','USD')),
    exchange TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_instruments_name ON instruments(name);
CREATE INDEX idx_instruments_amfi_code ON instruments(amfi_code);
CREATE INDEX idx_instruments_gfinance_symbol ON instruments(gfinance_symbol);


CREATE TABLE upload_history (
    id BIGSERIAL PRIMARY KEY,
    filename TEXT NOT NULL,
    file_size BIGINT,
    platform TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','PROCESSING','COMPLETED','FAILED','PARTIAL')),
    records_total INTEGER NOT NULL DEFAULT 0,
    records_imported INTEGER NOT NULL DEFAULT 0,
    records_duplicates INTEGER NOT NULL DEFAULT 0,
    records_errors INTEGER NOT NULL DEFAULT 0,
    error_details JSONB,
    uploaded_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

CREATE INDEX idx_upload_history_uploaded_at ON upload_history(uploaded_at DESC);


CREATE TABLE transactions (
    id BIGSERIAL PRIMARY KEY,
    instrument_id BIGINT NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    transaction_date DATE NOT NULL,
    transaction_type TEXT NOT NULL
        CHECK (transaction_type IN ('BUY','SELL','SWITCH_IN','SWITCH_OUT','DIVIDEND','BONUS','SPLIT')),
    quantity NUMERIC(18,6) NOT NULL,
    price NUMERIC(18,6) NOT NULL,
    amount NUMERIC(18,2) NOT NULL,
    platform TEXT NOT NULL
        CHECK (platform IN ('GROWW','ZERODHA','INDMONEY','MANUAL')),
    upload_id BIGINT REFERENCES upload_history(id) ON DELETE SET NULL,
    order_id TEXT,
    original_data JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_transactions_instrument_id ON transactions(instrument_id);
CREATE INDEX idx_transactions_transaction_date ON transactions(transaction_date);
CREATE INDEX idx_transactions_platform ON transactions(platform);
CREATE INDEX idx_transactions_order_id ON transactions(order_id);
CREATE INDEX idx_transactions_dedup
    ON transactions(instrument_id, transaction_date, transaction_type, platform);


CREATE TABLE import_logs (
    id BIGSERIAL PRIMARY KEY,
    upload_id BIGINT NOT NULL REFERENCES upload_history(id) ON DELETE CASCADE,
    row_number INTEGER,
    status TEXT NOT NULL
        CHECK (status IN ('IMPORTED','DUPLICATE','ERROR')),
    message TEXT,
    details JSONB
);

CREATE INDEX idx_import_logs_upload_id ON import_logs(upload_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS import_logs;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS upload_history;
DROP TABLE IF EXISTS instruments;
-- +goose StatementEnd
