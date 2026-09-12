-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION record_p2p_recipient_credit() RETURNS TRIGGER
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
 INSERT INTO transaction_status_events(tenant_id,aggregate_id,version,status,substatus,user_id,created_at)
 SELECT NEW.tenant_id,'p2p-incoming:' || recipient.id || ':' || c.idempotency_key,
   1,'completed','automated',recipient.user_id,NEW.created_at
 FROM ledger_transactions lt
 JOIN p2p_commands c ON c.tenant_id = lt.tenant_id AND c.idempotency_key = lt.idempotency_key
 JOIN wallets recipient ON recipient.tenant_id = c.tenant_id AND recipient.id = c.to_wallet_id
 JOIN wallets sender ON sender.tenant_id = c.tenant_id AND sender.id = c.from_wallet_id
 WHERE lt.tenant_id = NEW.tenant_id AND lt.id = NEW.transaction_id
   AND lt.reference_type = 'p2p' AND lt.status = 'completed'
   AND lt.reference_id = c.command->>'reference_id' AND lt.currency = c.command->>'currency'
   AND recipient.id = NEW.wallet_id AND NEW.amount = (c.command->>'amount')::bigint
   AND recipient.owner_type = 'user' AND recipient.user_id > 0
   AND recipient.user_id IS DISTINCT FROM sender.user_id;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER record_p2p_recipient_credit AFTER INSERT ON ledger_entries
FOR EACH ROW WHEN (NEW.entry_type = 'credit' AND NEW.status = 'completed')
EXECUTE FUNCTION record_p2p_recipient_credit();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Recipient status events are durable; use a forward migration';
END $$;
-- +goose StatementEnd
