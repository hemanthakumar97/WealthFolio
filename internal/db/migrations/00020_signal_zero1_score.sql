-- +goose Up
-- +goose StatementBegin
ALTER TABLE signal_results ADD COLUMN zero1_score INT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE signal_results DROP COLUMN IF EXISTS zero1_score;
-- +goose StatementEnd
