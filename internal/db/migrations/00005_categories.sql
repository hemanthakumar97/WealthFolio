-- +goose Up
-- +goose StatementBegin

CREATE TABLE categories (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    color       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_categories_name ON categories(name);

CREATE TABLE instrument_categories (
    id            BIGSERIAL PRIMARY KEY,
    instrument_id BIGINT NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    category_id   BIGINT NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    weight        NUMERIC(5,4) NOT NULL DEFAULT 1.0,
    UNIQUE (instrument_id, category_id)
);

CREATE INDEX idx_instrument_categories_instrument ON instrument_categories(instrument_id);
CREATE INDEX idx_instrument_categories_category ON instrument_categories(category_id);

CREATE TABLE instrument_allocations (
    id                 BIGSERIAL PRIMARY KEY,
    instrument_id      BIGINT NOT NULL UNIQUE REFERENCES instruments(id) ON DELETE CASCADE,
    target_percent     NUMERIC(5,2) NOT NULL DEFAULT 0,
    sip_amount         NUMERIC(12,2) NOT NULL DEFAULT 0,
    sip_target_percent NUMERIC(5,2) NOT NULL DEFAULT 0,
    alloc_category     TEXT NOT NULL DEFAULT 'OTHERS'
                           CHECK (alloc_category IN ('EQUITY','GOLD','DEBT','US_EQUITY','OTHERS')),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE category_allocations (
    id                 BIGSERIAL PRIMARY KEY,
    alloc_category     TEXT NOT NULL UNIQUE
                           CHECK (alloc_category IN ('EQUITY','GOLD','DEBT','US_EQUITY','OTHERS')),
    target_percent     NUMERIC(5,2) NOT NULL DEFAULT 0,
    sip_target_percent NUMERIC(5,2) NOT NULL DEFAULT 0,
    sip_amount         NUMERIC(12,2),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed the five canonical allocation categories so they always exist.
INSERT INTO category_allocations (alloc_category) VALUES
    ('EQUITY'), ('GOLD'), ('DEBT'), ('US_EQUITY'), ('OTHERS')
ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS category_allocations;
DROP TABLE IF EXISTS instrument_allocations;
DROP TABLE IF EXISTS instrument_categories;
DROP TABLE IF EXISTS categories;
-- +goose StatementEnd
