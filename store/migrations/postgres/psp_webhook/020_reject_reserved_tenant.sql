-- +goose Up
ALTER TABLE psp_configs
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE psp_transactions
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE psp_transaction_amounts
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE psp_config_overrides
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');
ALTER TABLE psp_interactions
  ADD CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default');

-- +goose Down
ALTER TABLE psp_interactions DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE psp_config_overrides DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE psp_transaction_amounts DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE psp_transactions DROP CONSTRAINT tenant_id_not_reserved;
ALTER TABLE psp_configs DROP CONSTRAINT tenant_id_not_reserved;
