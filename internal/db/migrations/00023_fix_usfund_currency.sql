-- +goose Up
-- +goose StatementBegin

-- US_FUND instruments were imported via INDMoney with amounts in USD, but the
-- instrument row was previously created (or matched by name) with currency='INR'
-- because maybePatch() never updated the currency field. This caused ClosedPositions
-- and Holdings calculators to skip the USD→INR conversion, showing raw USD prices.
--
-- Fix: set currency='USD' for every US_FUND instrument that still has currency='INR'.
-- This is safe because INDMoney only trades USD-denominated US stocks/ETFs; there are
-- no legitimate INR-denominated US_FUND instruments in this portfolio.

UPDATE instruments
   SET currency   = 'USD',
       updated_at = NOW()
 WHERE asset_type = 'US_FUND'
   AND currency   = 'INR';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

UPDATE instruments
   SET currency   = 'INR',
       updated_at = NOW()
 WHERE asset_type = 'US_FUND'
   AND currency   = 'USD';

-- +goose StatementEnd
