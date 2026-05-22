-- +goose Up
-- +goose StatementBegin
ALTER TABLE instruments DROP CONSTRAINT IF EXISTS instruments_asset_type_check;
ALTER TABLE instruments ADD CONSTRAINT instruments_asset_type_check
  CHECK (asset_type IN ('MF','ETF','STOCK','BOND','GOLD','OTHER','US_FUND'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE instruments DROP CONSTRAINT IF EXISTS instruments_asset_type_check;
ALTER TABLE instruments ADD CONSTRAINT instruments_asset_type_check
  CHECK (asset_type IN ('MF','ETF','STOCK','BOND','GOLD','OTHER'));
-- +goose StatementEnd
