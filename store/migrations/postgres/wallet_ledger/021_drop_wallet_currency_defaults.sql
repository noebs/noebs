-- +goose Up
ALTER TABLE wallets ALTER COLUMN currency DROP DEFAULT;
ALTER TABLE fee_configs ALTER COLUMN currency DROP DEFAULT;
ALTER TABLE transaction_limits ALTER COLUMN currency DROP DEFAULT;

-- +goose Down
SELECT 1;
