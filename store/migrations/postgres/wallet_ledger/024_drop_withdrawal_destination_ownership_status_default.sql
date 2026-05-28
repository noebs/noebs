-- +goose Up
ALTER TABLE withdrawal_destinations ALTER COLUMN ownership_status DROP DEFAULT;

-- +goose Down
SELECT 1;
