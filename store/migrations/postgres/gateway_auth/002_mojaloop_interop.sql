-- +goose Up
ALTER TABLE wallet_transaction_authorization_intents
 DROP CONSTRAINT wallet_transaction_authorization_intents_operation_check;
ALTER TABLE wallet_transaction_authorization_intents
 ADD CONSTRAINT wallet_transaction_authorization_intents_operation_check
 CHECK (operation IN ('wallet.p2p', 'wallet.withdrawal', 'wallet.interop.transfer'));

-- +goose Down
-- Refuse a downgrade if interop authorizations still exist; do not discard them.
ALTER TABLE wallet_transaction_authorization_intents
 DROP CONSTRAINT wallet_transaction_authorization_intents_operation_check;
ALTER TABLE wallet_transaction_authorization_intents
 ADD CONSTRAINT wallet_transaction_authorization_intents_operation_check
 CHECK (operation IN ('wallet.p2p', 'wallet.withdrawal'));
