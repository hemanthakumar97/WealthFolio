-- +goose Up
-- +goose StatementBegin
ALTER TABLE instruments DROP CONSTRAINT IF EXISTS instruments_asset_type_check;
UPDATE instruments SET asset_type = 'METAL' WHERE asset_type = 'GOLD';
ALTER TABLE instruments ADD CONSTRAINT instruments_asset_type_check
  CHECK (asset_type IN ('MF','ETF','STOCK','BOND','METAL','OTHER','US_FUND'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE instruments DROP CONSTRAINT IF EXISTS instruments_asset_type_check;
UPDATE instruments SET asset_type = 'GOLD' WHERE asset_type = 'METAL';
ALTER TABLE instruments ADD CONSTRAINT instruments_asset_type_check
  CHECK (asset_type IN ('MF','ETF','STOCK','BOND','GOLD','OTHER','US_FUND'));
-- +goose StatementEnd
