-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
  tenant_table text;
BEGIN
  IF to_regclass('tenants') IS NOT NULL
    AND NOT EXISTS (
      SELECT 1 FROM pg_constraint
      WHERE conrelid = to_regclass('tenants')
        AND conname = 'tenant_id_not_reserved'
    )
  THEN
    ALTER TABLE tenants
      ADD CONSTRAINT tenant_id_not_reserved
      CHECK (lower(btrim(id)) <> 'default');
  END IF;

  FOREACH tenant_table IN ARRAY ARRAY[
    'users',
    'auth_accounts',
    'cards',
    'cache_cards',
    'cache_billers',
    'beneficiaries',
    'tokens',
    'transactions',
    'push_data',
    'api_keys',
    'login_metrics',
    'meter_names',
    'kyc',
    'passports',
    'merchant_issues',
    'wallets',
    'ledger_transactions',
    'ledger_entries',
    'balance_holds',
    'fee_configs',
    'exchange_rates',
    'transaction_limits',
    'psp_configs',
    'psp_transactions',
    'admin_roles',
    'admin_users',
    'manual_transfers',
    'manual_transfer_approvals',
    'wallet_audit_log',
    'funding_sources',
    'ledger_funding_links',
    'withdrawal_destinations',
    'ownership_verifications',
    'psp_transaction_amounts',
    'psp_config_overrides',
    'wallet_user_2fa',
    'psp_interactions'
  ]
  LOOP
    IF to_regclass(tenant_table) IS NULL THEN
      CONTINUE;
    END IF;

    IF EXISTS (
      SELECT 1 FROM information_schema.columns
      WHERE table_schema = current_schema()
        AND table_name = tenant_table
        AND column_name = 'tenant_id'
    ) AND NOT EXISTS (
      SELECT 1 FROM pg_constraint
      WHERE conrelid = to_regclass(tenant_table)
        AND conname = 'tenant_id_not_reserved'
    )
    THEN
      EXECUTE format(
        'ALTER TABLE %I ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> %L)',
        tenant_table,
        'default'
      );
    END IF;
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
  tenant_table text;
BEGIN
  IF to_regclass('tenants') IS NOT NULL THEN
    ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenant_id_not_reserved;
  END IF;

  FOREACH tenant_table IN ARRAY ARRAY[
    'users',
    'auth_accounts',
    'cards',
    'cache_cards',
    'cache_billers',
    'beneficiaries',
    'tokens',
    'transactions',
    'push_data',
    'api_keys',
    'login_metrics',
    'meter_names',
    'kyc',
    'passports',
    'merchant_issues',
    'wallets',
    'ledger_transactions',
    'ledger_entries',
    'balance_holds',
    'fee_configs',
    'exchange_rates',
    'transaction_limits',
    'psp_configs',
    'psp_transactions',
    'admin_roles',
    'admin_users',
    'manual_transfers',
    'manual_transfer_approvals',
    'wallet_audit_log',
    'funding_sources',
    'ledger_funding_links',
    'withdrawal_destinations',
    'ownership_verifications',
    'psp_transaction_amounts',
    'psp_config_overrides',
    'wallet_user_2fa',
    'psp_interactions'
  ]
  LOOP
    IF to_regclass(tenant_table) IS NOT NULL THEN
      EXECUTE format('ALTER TABLE %I DROP CONSTRAINT IF EXISTS tenant_id_not_reserved', tenant_table);
    END IF;
  END LOOP;
END $$;
-- +goose StatementEnd
