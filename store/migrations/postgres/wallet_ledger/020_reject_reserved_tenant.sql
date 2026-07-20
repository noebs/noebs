-- +goose Up
ALTER TABLE wallets
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE ledger_transactions
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE ledger_entries
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE balance_holds
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE fee_configs
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE exchange_rates
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE transaction_limits
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE manual_transfers
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE manual_transfer_approvals
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE wallet_audit_log
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE funding_sources
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE ledger_funding_links
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE withdrawal_destinations
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE ownership_verifications
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE wallet_user_2fa
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');

-- +goose Down
ALTER TABLE wallet_user_2fa DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE ownership_verifications DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE withdrawal_destinations DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE ledger_funding_links DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE funding_sources DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE wallet_audit_log DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE manual_transfer_approvals DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE manual_transfers DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE transaction_limits DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE exchange_rates DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE fee_configs DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE balance_holds DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE ledger_entries DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE ledger_transactions DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE wallets DROP CONSTRAINT tenant_id_not_reserved;
