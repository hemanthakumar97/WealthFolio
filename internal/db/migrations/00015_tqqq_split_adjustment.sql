-- +goose Up
-- +goose StatementBegin
-- Apply 2-for-1 stock split for ProShares UltraPro QQQ (TQQQ) occurring on Nov 20, 2025.
-- Only adjusts if unadjusted transactions exist (price > 100 before the split date).
DO $$
DECLARE
    tqqq_id BIGINT;
    needs_adjustment BOOLEAN;
BEGIN
    SELECT id INTO tqqq_id FROM instruments WHERE name = 'ProShares UltraPro QQQ' LIMIT 1;

    IF tqqq_id IS NOT NULL THEN
        SELECT EXISTS (
            SELECT 1 FROM transactions
            WHERE instrument_id = tqqq_id
              AND transaction_date >= '2025-10-01'
              AND transaction_date < '2025-11-20'
              AND price > 100
        ) INTO needs_adjustment;

        IF needs_adjustment THEN
            UPDATE transactions
            SET quantity = quantity * 2,
                price    = price / 2
            WHERE instrument_id = tqqq_id
              AND transaction_date < '2025-11-20';
        END IF;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    tqqq_id BIGINT;
BEGIN
    SELECT id INTO tqqq_id FROM instruments WHERE name = 'ProShares UltraPro QQQ' LIMIT 1;

    IF tqqq_id IS NOT NULL THEN
        UPDATE transactions
        SET quantity = quantity / 2,
            price    = price * 2
        WHERE instrument_id = tqqq_id
          AND transaction_date < '2025-11-20';
    END IF;
END $$;
-- +goose StatementEnd
