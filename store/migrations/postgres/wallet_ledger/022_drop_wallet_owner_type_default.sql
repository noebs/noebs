-- +goose Up
ALTER TABLE wallets ALTER COLUMN owner_type DROP DEFAULT;

-- +goose Down
SELECT 1;
