-- +goose Up
-- +goose StatementBegin

CREATE TABLE email_watch_rules (
    id            BIGSERIAL    PRIMARY KEY,
    name          TEXT         NOT NULL,
    platform      TEXT         NOT NULL,
    from_email    TEXT         NOT NULL,
    subject_query TEXT         NOT NULL,   -- Gmail query fragment for subject
    parser_type   TEXT         NOT NULL,   -- groww_mf | zerodha_contract_note | indmoney_us
    enabled       BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Seed default rules
INSERT INTO email_watch_rules (name, platform, from_email, subject_query, parser_type) VALUES
(
    'Groww MF – Units Allocated',
    'GROWW',
    'noreply@groww.in',
    'subject:allocated OR subject:allotted OR subject:SIP',
    'groww_mf'
),
(
    'Zerodha – Equity Contract Note',
    'ZERODHA',
    'no-reply-contract-notes@reportsmailer.zerodha.net',
    'subject:"Contract Note"',
    'zerodha_contract_note'
),
(
    'IndMoney – US Stock SIP',
    'INDMONEY',
    'transactions@transactions.indmoney.com',
    'subject:"SIP instalment" OR subject:"SIP installment" subject:successful',
    'indmoney_us'
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS email_watch_rules;
-- +goose StatementEnd
