// Package tenantcutover implements the reviewed, one-time tenant-mojaloop to
// noebs administrative cutover. It is deliberately not a general tenant merge.
package tenantcutover

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

const Source = "tenant-mojaloop"
const Target = "noebs"

var ErrUnsafe = errors.New("tenant cutover precondition failed")

type Table struct {
	Name         string           `json:"name"`
	TenantScoped bool             `json:"tenant_scoped"`
	Rows         int64            `json:"rows"`
	Tenants      map[string]int64 `json:"tenants,omitempty"`
	SHA256       string           `json:"sha256_excluding_tenant_id"`
}

type Snapshot struct {
	Database     string   `json:"database"`
	Source       string   `json:"source"`
	Target       string   `json:"target"`
	Catalog      []string `json:"catalog"`
	SchemaSHA256 string   `json:"schema_sha256"`
	Tables       []Table  `json:"tables"`
}

type foreignKey struct {
	table, name         string
	deferred, initially bool
}
type trigger struct{ table, name, enabled string }

// Run returns a non-sensitive row-count and content-hash manifest. Applying
// requires an exact previously reviewed manifest and an administrative owner
// connection. Services and workers must remain stopped across all databases.
func Run(ctx context.Context, db *sql.DB, apply bool, expected *Snapshot) (Snapshot, error) {
	isolation := sql.LevelRepeatableRead
	if apply {
		isolation = sql.LevelReadCommitted
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: isolation, ReadOnly: !apply})
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='10s'; SET LOCAL statement_timeout='120s'; SET LOCAL search_path=pg_catalog,public; SET LOCAL timezone='UTC'`); err != nil {
		return Snapshot{}, err
	}
	tables, err := listTables(ctx, tx)
	if err != nil {
		return Snapshot{}, err
	}
	if apply {
		// Lock even unscoped tables: triggers must not race with receipt creation,
		// and the manifest must cover all data rather than just obvious tenant FKs.
		for _, table := range tables {
			if _, err = tx.ExecContext(ctx, `LOCK TABLE public.`+quote(table)+` IN ACCESS EXCLUSIVE MODE`); err != nil {
				return Snapshot{}, err
			}
		}
	}
	before, err := snapshot(ctx, tx, tables)
	if err != nil {
		return Snapshot{}, err
	}
	if err = validate(ctx, tx, before); err != nil {
		return before, err
	}
	if !apply {
		return before, tx.Commit()
	}
	if expected == nil || !reflect.DeepEqual(before, *expected) {
		return before, fmt.Errorf("%w: current database differs from expected manifest", ErrUnsafe)
	}
	keys, triggers, err := protections(ctx, tx)
	if err != nil {
		return before, err
	}
	for _, key := range keys {
		if _, err = tx.ExecContext(ctx, `ALTER TABLE public.`+quote(key.table)+` ALTER CONSTRAINT `+quote(key.name)+` DEFERRABLE INITIALLY DEFERRED`); err != nil {
			return before, err
		}
	}
	if _, err = tx.ExecContext(ctx, `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		return before, err
	}
	for _, trigger := range triggers {
		if _, err = tx.ExecContext(ctx, `ALTER TABLE public.`+quote(trigger.table)+` DISABLE TRIGGER `+quote(trigger.name)); err != nil {
			return before, err
		}
	}
	// Reuse the old catalog timestamp; no application/profile/wallet ID changes.
	if _, err = tx.ExecContext(ctx, `INSERT INTO public.tenants(id,name,created_at) SELECT $2,'Noebs',created_at FROM public.tenants WHERE id=$1`, Source, Target); err != nil {
		return before, err
	}
	for _, table := range before.Tables {
		if table.TenantScoped {
			if _, err = tx.ExecContext(ctx, `UPDATE public.`+quote(table.Name)+` SET tenant_id=$2 WHERE tenant_id=$1`, Source, Target); err != nil {
				return before, fmt.Errorf("move %s: %w", table.Name, err)
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM public.tenants WHERE id IN ($1,'tenant-cutover','tenant-sandbox')`, Source); err != nil {
		return before, err
	}
	// Drain every FK check before restoring original deferral modes. Internal
	// constraint triggers have remained enabled for the entire operation.
	if _, err = tx.ExecContext(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		return before, err
	}
	for _, key := range keys {
		mode := `NOT DEFERRABLE INITIALLY IMMEDIATE`
		if key.deferred {
			mode = `DEFERRABLE INITIALLY IMMEDIATE`
			if key.initially {
				mode = `DEFERRABLE INITIALLY DEFERRED`
			}
		}
		if _, err = tx.ExecContext(ctx, `ALTER TABLE public.`+quote(key.table)+` ALTER CONSTRAINT `+quote(key.name)+` `+mode); err != nil {
			return before, err
		}
	}
	for _, trigger := range triggers {
		mode := map[string]string{"O": "ENABLE", "D": "DISABLE", "R": "ENABLE REPLICA", "A": "ENABLE ALWAYS"}[trigger.enabled]
		if mode == "" {
			return before, fmt.Errorf("%w: unknown trigger state", ErrUnsafe)
		}
		if _, err = tx.ExecContext(ctx, `ALTER TABLE public.`+quote(trigger.table)+` `+mode+` TRIGGER `+quote(trigger.name)); err != nil {
			return before, err
		}
	}
	after, err := snapshot(ctx, tx, tables)
	if err != nil {
		return before, err
	}
	want := before
	want.Catalog = []string{Target}
	want.Tables = append([]Table(nil), before.Tables...)
	for i := range want.Tables {
		if want.Tables[i].TenantScoped && want.Tables[i].Rows > 0 {
			want.Tables[i].Tenants = map[string]int64{Target: want.Tables[i].Rows}
		}
	}
	if !reflect.DeepEqual(want, after) {
		return before, fmt.Errorf("%w: post-cutover content or schema differs; transaction rolled back", ErrUnsafe)
	}
	if err = tx.Commit(); err != nil {
		return before, err
	}
	return after, nil
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func listTables(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.relname,c.relkind::text,c.relrowsecurity,c.relhassubclass FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p') ORDER BY c.relname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name, kind string
		var rls, children bool
		if err = rows.Scan(&name, &kind, &rls, &children); err != nil {
			return nil, err
		}
		if kind != "r" || rls || children {
			return nil, fmt.Errorf("%w: unsupported table layout %s", ErrUnsafe, name)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func snapshot(ctx context.Context, tx *sql.Tx, tables []string) (Snapshot, error) {
	s := Snapshot{Source: Source, Target: Target}
	if err := tx.QueryRowContext(ctx, `SELECT current_database()`).Scan(&s.Database); err != nil {
		return s, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM public.tenants ORDER BY id`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return s, err
		}
		s.Catalog = append(s.Catalog, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return s, err
	}
	rows.Close()
	// Definitions and trigger states are restored exactly, including initially
	// deferred constraints. Schema changes between plan/apply invalidate the plan.
	s.SchemaSHA256, _, err = digest(ctx, tx, `SELECT value FROM (
SELECT 'constraint:'||c.conrelid::regclass::text||':'||c.conname||':'||pg_get_constraintdef(c.oid)||':'||c.convalidated::text AS value FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE n.nspname='public'
UNION ALL SELECT 'trigger:'||t.tgrelid::regclass::text||':'||pg_get_triggerdef(t.oid)||':'||t.tgenabled::text FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND NOT t.tgisinternal
UNION ALL SELECT 'column:'||table_name||':'||column_name||':'||data_type||':'||is_nullable||':'||COALESCE(column_default,'')||':'||COALESCE(generation_expression,'') FROM information_schema.columns WHERE table_schema='public') d ORDER BY value COLLATE "C"`)
	if err != nil {
		return s, err
	}
	for _, name := range tables {
		if name == "tenants" {
			continue
		}
		t := Table{Name: name}
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name='tenant_id')`, name).Scan(&t.TenantScoped); err != nil {
			return s, err
		}
		expr := `to_jsonb(t)`
		if t.TenantScoped {
			expr += `-'tenant_id'`
		}
		// json columns retain protocol bytes/whitespace; to_jsonb alone would
		// normalize them and miss an accidental change to signed wire data.
		jsonRows, jsonErr := tx.QueryContext(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND data_type='json' ORDER BY ordinal_position`, name)
		if jsonErr != nil {
			return s, jsonErr
		}
		for jsonRows.Next() {
			var column string
			if jsonErr = jsonRows.Scan(&column); jsonErr != nil {
				jsonRows.Close()
				return s, jsonErr
			}
			expr = `(` + expr + `) || jsonb_build_object('` + strings.ReplaceAll(column, "'", "''") + `',t.` + quote(column) + `::text)`
		}
		if jsonErr = jsonRows.Err(); jsonErr != nil {
			jsonRows.Close()
			return s, jsonErr
		}
		jsonRows.Close()
		t.SHA256, t.Rows, err = digest(ctx, tx, `SELECT value FROM (SELECT (`+expr+`)::text AS value FROM public.`+quote(name)+` t) d ORDER BY value COLLATE "C"`)
		if err != nil {
			return s, err
		}
		if t.TenantScoped {
			if t.Rows > 0 {
				t.Tenants = map[string]int64{}
			}
			rows, err = tx.QueryContext(ctx, `SELECT tenant_id,count(*) FROM public.`+quote(name)+` GROUP BY tenant_id ORDER BY tenant_id`)
			if err != nil {
				return s, err
			}
			for rows.Next() {
				var id string
				var count int64
				if err = rows.Scan(&id, &count); err != nil {
					rows.Close()
					return s, err
				}
				t.Tenants[id] = count
			}
			if err = rows.Err(); err != nil {
				rows.Close()
				return s, err
			}
			rows.Close()
		}
		s.Tables = append(s.Tables, t)
	}
	return s, nil
}

func digest(ctx context.Context, tx *sql.Tx, query string) (string, int64, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	h := sha256.New()
	var count int64
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return "", 0, err
		}
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		h.Write(size[:])
		h.Write([]byte(value))
		count++
	}
	return hex.EncodeToString(h.Sum(nil)), count, rows.Err()
}

func protections(ctx context.Context, tx *sql.Tx) ([]foreignKey, []trigger, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.relname,k.conname,k.condeferrable,k.condeferred,k.convalidated,k.confupdtype::text,n2.nspname FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_class c2 ON c2.oid=k.confrelid JOIN pg_namespace n2 ON n2.oid=c2.relnamespace WHERE n.nspname='public' AND k.contype='f' ORDER BY c.relname,k.conname`)
	if err != nil {
		return nil, nil, err
	}
	var keys []foreignKey
	for rows.Next() {
		var k foreignKey
		var valid bool
		var update, ns string
		if err = rows.Scan(&k.table, &k.name, &k.deferred, &k.initially, &valid, &update, &ns); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if !valid || update != "a" || ns != "public" {
			rows.Close()
			return nil, nil, fmt.Errorf("%w: unsupported FK %s.%s", ErrUnsafe, k.table, k.name)
		}
		keys = append(keys, k)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT c.relname,t.tgname,t.tgenabled::text FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND NOT t.tgisinternal ORDER BY c.relname,t.tgname`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var triggers []trigger
	for rows.Next() {
		var t trigger
		if err = rows.Scan(&t.table, &t.name, &t.enabled); err != nil {
			return nil, nil, err
		}
		triggers = append(triggers, t)
	}
	return keys, triggers, rows.Err()
}

func validate(ctx context.Context, tx *sql.Tx, s Snapshot) error {
	knownDB := map[string]bool{"identity_auth": true, "wallet_ledger": true, "card_vault": true, "admin_reporting": true, "notification_chat": true, "ebs_adapter": true, "gateway_auth": true}
	if !knownDB[s.Database] {
		return fmt.Errorf("%w: unsupported database %s", ErrUnsafe, s.Database)
	}
	sourceFound := false
	for _, id := range s.Catalog {
		switch id {
		case Source:
			sourceFound = true
		case "tenant-cutover", "tenant-sandbox":
		case Target:
			return fmt.Errorf("%w: target catalog already exists; no merges or implicit reruns", ErrUnsafe)
		default:
			return fmt.Errorf("%w: unreviewed catalog tenant %s", ErrUnsafe, id)
		}
	}
	if !sourceFound {
		return fmt.Errorf("%w: expected source catalog missing", ErrUnsafe)
	}
	// This cutover was reviewed against the existing Noebs personal account and
	// settled Mojaloop data. Unreviewed workflow, evidence and encrypted auth data
	// are refused even if a generic tenant_id update would satisfy foreign keys.
	reviewed := map[string]bool{"users": true, "identity_verifications": true, "account_enrollments": true, "wallets": true, "balance_holds": true, "fee_configs": true, "interop_aliases": true, "interop_bindings": true, "interop_inbox": true, "interop_quotes": true, "interop_transfers": true, "ledger_entries": true, "ledger_transactions": true, "shared_outbound_limits": true, "transaction_limit_period_usage": true, "transaction_limit_reservations": true, "transaction_limits": true, "wallet_audit_log": true}
	tables := map[string]Table{}
	for _, t := range s.Tables {
		tables[t.Name] = t
		for id, n := range t.Tenants {
			if n > 0 && id != Source {
				return fmt.Errorf("%w: %s has rows in unexpected tenant %s", ErrUnsafe, t.Name, id)
			}
		}
		if t.TenantScoped && t.Rows > 0 && !reviewed[t.Name] {
			return fmt.Errorf("%w: nonempty table requires separate review: %s", ErrUnsafe, t.Name)
		}
		if (strings.HasPrefix(t.Name, "backoffice_") || t.Name == "wallet_transaction_authorization_flows") && t.Rows > 0 {
			return fmt.Errorf("%w: encrypted auth sessions/flows must be empty", ErrUnsafe)
		}
	}
	checks := map[string]string{
		"balance_holds":                  `status IN ('active','committed') OR amount_remaining <> 0`,
		"interop_transfers":              `status NOT IN ('SUCCEEDED','FAILED') OR (lease_until IS NOT NULL AND lease_until > clock_timestamp())`,
		"interop_quotes":                 `status IN ('REQUESTED','QUOTING') OR (lease_until IS NOT NULL AND lease_until > clock_timestamp()) OR (status='READY' AND expires_at > clock_timestamp() AND NOT EXISTS(SELECT FROM public.interop_quote_closures c WHERE c.tenant_id=interop_quotes.tenant_id AND c.quote_id=interop_quotes.id))`,
		"interop_inbox":                  `applied_at IS NULL`,
		"transaction_limit_reservations": `status='reserved'`,
		"ledger_transactions":            `status NOT IN ('completed','reversed')`,
	}
	for name, predicate := range checks {
		if _, ok := tables[name]; !ok {
			continue
		}
		var n int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM public.`+quote(name)+` WHERE `+predicate).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: %s has %d unsettled records", ErrUnsafe, name, n)
		}
	}
	_, _, err := protections(ctx, tx)
	return err
}
