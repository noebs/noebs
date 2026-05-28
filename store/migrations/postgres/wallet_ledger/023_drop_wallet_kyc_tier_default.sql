-- +goose Up
ALTER TABLE wallets ALTER COLUMN kyc_tier DROP DEFAULT;

-- +goose Down
SELECT 1;
