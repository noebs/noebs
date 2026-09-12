-- Execute inside an operator transaction after the new ledger/worker image has
-- converged. The caller supplies noebs.p2p_policy (JSON) and noebs.p2p_action
-- (activate or rollback) with SET LOCAL. Roll back the transaction for a dry run.
-- This is configuration only: no customer, journal, hold or usage row is changed.
DO $policy$
DECLARE
 p jsonb := current_setting('noebs.p2p_policy',true)::jsonb;
 action text := current_setting('noebs.p2p_action',true);
 source transaction_limits;
 local_limit transaction_limits;
 shared shared_outbound_limits;
 fee fee_configs;
 operator_id bigint;
 event_key text;
 n integer;
 was_applied boolean;
 was_rolled_back boolean;
 before_state jsonb;
 after_state jsonb;
 expected_enabled boolean;
BEGIN
 IF p IS NULL OR jsonb_typeof(p)<>'object' OR action IS NULL
   OR action NOT IN ('activate','rollback') OR p->>'tenant_id' IS NULL
   OR p->>'kyc_tier' IS NULL OR p->>'currency' IS NULL
   OR p->>'currency_unit_version_id' IS NULL OR p->>'daily_limit' IS NULL
   OR p->>'monthly_limit' IS NULL OR p->>'per_transaction_limit' IS NULL
   OR p->>'request_id' IS NULL OR p->>'operator_label' IS NULL
   OR p->>'request_id'='' OR p->>'operator_label'=''
   OR (p->>'currency_unit_version_id')::bigint<=0
   OR (p->>'daily_limit')::bigint<=0 OR (p->>'monthly_limit')::bigint<(p->>'daily_limit')::bigint
   OR (p->>'per_transaction_limit')::bigint<=0 OR (p->>'per_transaction_limit')::bigint>(p->>'daily_limit')::bigint THEN
   RAISE EXCEPTION 'explicit reviewed P2P policy parameters are required';
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('shared-p2p-policy:'||(p->>'tenant_id'),0));
 -- The common reservation path and monetary posting already lock wallets.
 -- Holding them here gives activation a boundary with in-flight admissions.
 PERFORM id FROM wallets WHERE tenant_id=p->>'tenant_id' ORDER BY id FOR UPDATE;
 SELECT * INTO STRICT source FROM transaction_limits
 WHERE tenant_id=p->>'tenant_id' AND kyc_tier=p->>'kyc_tier'
   AND currency=p->>'currency' AND currency_unit_version_id=(p->>'currency_unit_version_id')::bigint
   AND transaction_type='interop_out' FOR UPDATE;
 IF NOT source.is_active OR source.daily_limit<>(p->>'daily_limit')::bigint
   OR source.monthly_limit<>(p->>'monthly_limit')::bigint
   OR source.per_transaction_limit<>(p->>'per_transaction_limit')::bigint THEN
   RAISE EXCEPTION 'existing outbound policy differs from reviewed baseline';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM currency_unit_versions WHERE id=source.currency_unit_version_id
   AND currency_code=source.currency AND iso_minor_exponent=2 AND display_exponent=2) THEN
   RAISE EXCEPTION 'reviewed currency unit differs';
 END IF;
 -- Fees are scoped by currency/unit, not KYC tier. Never enable or disable
 -- another tier's local admission while configuring this one scoped policy.
 IF EXISTS(SELECT 1 FROM transaction_limits WHERE tenant_id=source.tenant_id
   AND transaction_type='p2p' AND currency=source.currency
   AND currency_unit_version_id=source.currency_unit_version_id
   AND kyc_tier<>source.kyc_tier AND is_active) THEN
   RAISE EXCEPTION 'another tier has active local policy; separate fee review required';
 END IF;
 SELECT count(*) INTO n FROM wallet_audit_log WHERE tenant_id=source.tenant_id
   AND request_id=p->>'request_id' AND event_type='shared_p2p_policy_activated';
 IF n>1 THEN RAISE EXCEPTION 'ambiguous activation audit'; END IF;
 IF n=1 AND NOT EXISTS(SELECT 1 FROM wallet_audit_log WHERE tenant_id=source.tenant_id
   AND request_id=p->>'request_id' AND event_type='shared_p2p_policy_activated'
   AND metadata->'policy_input'=p) THEN
   RAISE EXCEPTION 'activation request identity reused with changed policy';
 END IF;
 was_applied := n=1;
 SELECT count(*) INTO n FROM wallet_audit_log WHERE tenant_id=source.tenant_id
   AND request_id=(p->>'request_id')||':rollback' AND event_type='shared_p2p_policy_rolled_back';
 IF n>1 THEN RAISE EXCEPTION 'ambiguous rollback audit'; END IF;
 IF n=1 AND NOT EXISTS(SELECT 1 FROM wallet_audit_log WHERE tenant_id=source.tenant_id
   AND request_id=(p->>'request_id')||':rollback' AND event_type='shared_p2p_policy_rolled_back'
   AND metadata->'policy_input'=p) THEN
   RAISE EXCEPTION 'rollback request identity reused with changed policy';
 END IF;
 was_rolled_back := n=1;
 IF was_rolled_back AND action='activate' THEN
   RAISE EXCEPTION 'reactivation after rollback requires a new reviewed policy operation';
 END IF;

 SELECT jsonb_build_object(
   'wallets',(SELECT jsonb_agg(to_jsonb(w) ORDER BY w.id) FROM wallets w WHERE w.tenant_id=source.tenant_id),
   'periods',(SELECT jsonb_agg(to_jsonb(u) ORDER BY u.wallet_id,u.transaction_type,u.period_kind,u.period_start) FROM transaction_limit_period_usage u WHERE u.tenant_id=source.tenant_id),
   'reservations',(SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id) FROM transaction_limit_reservations r WHERE r.tenant_id=source.tenant_id),
   'journals',(SELECT jsonb_agg(to_jsonb(j) ORDER BY j.id) FROM ledger_transactions j WHERE j.tenant_id=source.tenant_id),
   'entries',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM ledger_entries e WHERE e.tenant_id=source.tenant_id),
   'holds',(SELECT jsonb_agg(to_jsonb(h) ORDER BY h.id) FROM balance_holds h WHERE h.tenant_id=source.tenant_id)) INTO before_state;

 IF NOT was_applied THEN
   IF action='rollback' THEN RAISE EXCEPTION 'no matching activation to roll back'; END IF;
   IF EXISTS(SELECT 1 FROM fee_configs WHERE tenant_id=source.tenant_id AND transaction_type='p2p'
      AND currency=source.currency AND currency_unit_version_id=source.currency_unit_version_id)
     OR EXISTS(SELECT 1 FROM transaction_limits WHERE tenant_id=source.tenant_id AND transaction_type='p2p'
      AND kyc_tier=source.kyc_tier AND currency=source.currency AND currency_unit_version_id=source.currency_unit_version_id)
     OR EXISTS(SELECT 1 FROM shared_outbound_limits WHERE tenant_id=source.tenant_id
      AND kyc_tier=source.kyc_tier AND currency=source.currency AND currency_unit_version_id=source.currency_unit_version_id) THEN
     RAISE EXCEPTION 'existing local/shared policy requires separate review';
   END IF;
   -- This is an explicitly identified database-maintenance audit principal,
   -- not an impersonated human/OIDC user. It grants no application privileges.
   INSERT INTO operator_identities(issuer,subject)
     VALUES('urn:noebs:database-maintenance',current_database()||'/'||session_user)
     ON CONFLICT(issuer,subject) DO NOTHING;
   SELECT id INTO STRICT operator_id FROM operator_identities
     WHERE issuer='urn:noebs:database-maintenance' AND subject=current_database()||'/'||session_user;
   INSERT INTO fee_configs(tenant_id,transaction_type,currency,currency_unit_version_id,
     tier_min,tier_max,percentage_fee,flat_fee,min_fee,max_fee,fee_account_code,is_active,created_by_operator_id)
     VALUES(source.tenant_id,'p2p',source.currency,source.currency_unit_version_id,0,NULL,0,0,0,NULL,NULL,true,operator_id);
   INSERT INTO transaction_limits(tenant_id,kyc_tier,transaction_type,currency,currency_unit_version_id,
     daily_limit,monthly_limit,per_transaction_limit,is_active)
     VALUES(source.tenant_id,source.kyc_tier,'p2p',source.currency,source.currency_unit_version_id,
       source.daily_limit,source.monthly_limit,source.per_transaction_limit,true);
   INSERT INTO shared_outbound_limits(tenant_id,kyc_tier,currency,currency_unit_version_id,daily_limit,monthly_limit,enabled)
     VALUES(source.tenant_id,source.kyc_tier,source.currency,source.currency_unit_version_id,source.daily_limit,source.monthly_limit,true);
 END IF;

 SELECT * INTO STRICT local_limit FROM transaction_limits WHERE tenant_id=source.tenant_id AND transaction_type='p2p'
   AND kyc_tier=source.kyc_tier AND currency=source.currency AND currency_unit_version_id=source.currency_unit_version_id FOR UPDATE;
 SELECT * INTO STRICT shared FROM shared_outbound_limits WHERE tenant_id=source.tenant_id
   AND kyc_tier=source.kyc_tier AND currency=source.currency AND currency_unit_version_id=source.currency_unit_version_id FOR UPDATE;
 SELECT * INTO STRICT fee FROM fee_configs WHERE tenant_id=source.tenant_id AND transaction_type='p2p'
   AND currency=source.currency AND currency_unit_version_id=source.currency_unit_version_id FOR UPDATE;
 expected_enabled := NOT was_rolled_back;
 IF local_limit.daily_limit<>source.daily_limit OR local_limit.monthly_limit<>source.monthly_limit
   OR local_limit.per_transaction_limit<>source.per_transaction_limit OR local_limit.is_active<>expected_enabled
   OR NOT shared.enabled OR shared.daily_limit<>source.daily_limit OR shared.monthly_limit<>source.monthly_limit
   OR fee.tier_min<>0 OR fee.tier_max IS NOT NULL OR fee.percentage_fee<>0 OR fee.flat_fee<>0 OR fee.min_fee<>0
   OR fee.max_fee IS NOT NULL OR fee.fee_account_code IS NOT NULL OR fee.is_active<>expected_enabled THEN
   RAISE EXCEPTION 'local/shared configuration changed; refusing mutation or retry';
 END IF;
 IF action='rollback' AND NOT was_rolled_back THEN
   UPDATE fee_configs SET is_active=false WHERE id=fee.id;
   UPDATE transaction_limits SET is_active=false WHERE id=local_limit.id;
   -- Keep shared enforcement enabled: earlier local spending still consumes
   -- the same outbound daily/monthly allowance after local sending is disabled.
 END IF;
 IF (action='activate' AND NOT was_applied) OR (action='rollback' AND NOT was_rolled_back) THEN
   event_key := p->>'request_id';
   IF action='rollback' THEN event_key := event_key||':rollback'; END IF;
   INSERT INTO wallet_audit_log(tenant_id,event_type,actor_type,actor_id,target_type,target_id,
     action,old_value,new_value,metadata,request_id)
   VALUES(source.tenant_id,CASE WHEN action='activate' THEN 'shared_p2p_policy_activated' ELSE 'shared_p2p_policy_rolled_back' END,
     'operator',p->>'operator_label','shared_outbound_policy',source.kyc_tier||':'||source.currency||':'||source.currency_unit_version_id,
     action,jsonb_build_object('interop_out_policy',to_jsonb(source)),
     jsonb_build_object('local_sending_enabled',action='activate','shared_budget_enabled',true,'fee_minor',0,
       'per_transaction_limit',source.per_transaction_limit,'daily_limit',source.daily_limit,'monthly_limit',source.monthly_limit),
     jsonb_build_object('database_actor',session_user,'database_role',current_user,
       'members',ARRAY['interop_out','p2p'],'existing_usage_preserved',true,'policy_input',p),event_key);
 END IF;
 SELECT jsonb_build_object(
   'wallets',(SELECT jsonb_agg(to_jsonb(w) ORDER BY w.id) FROM wallets w WHERE w.tenant_id=source.tenant_id),
   'periods',(SELECT jsonb_agg(to_jsonb(u) ORDER BY u.wallet_id,u.transaction_type,u.period_kind,u.period_start) FROM transaction_limit_period_usage u WHERE u.tenant_id=source.tenant_id),
   'reservations',(SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id) FROM transaction_limit_reservations r WHERE r.tenant_id=source.tenant_id),
   'journals',(SELECT jsonb_agg(to_jsonb(j) ORDER BY j.id) FROM ledger_transactions j WHERE j.tenant_id=source.tenant_id),
   'entries',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM ledger_entries e WHERE e.tenant_id=source.tenant_id),
   'holds',(SELECT jsonb_agg(to_jsonb(h) ORDER BY h.id) FROM balance_holds h WHERE h.tenant_id=source.tenant_id)) INTO after_state;
 IF before_state IS DISTINCT FROM after_state THEN
   RAISE EXCEPTION 'account, money or usage changed during policy operation';
 END IF;
END $policy$;
