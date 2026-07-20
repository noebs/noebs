package postgresauthority

// Role is one login boundary in the Noebs service-database cluster.
type Role struct {
	Name      string
	Database  string
	Migration bool
}

var catalog = []Role{
	{Name: "admin_reporting_migrate", Database: "admin_reporting", Migration: true},
	{Name: "admin_reporting_projector", Database: "admin_reporting"},
	{Name: "admin_reporting_runtime", Database: "admin_reporting"},
	{Name: "card_vault_migrate", Database: "card_vault", Migration: true},
	{Name: "card_vault_runtime", Database: "card_vault"},
	{Name: "ebs_adapter_events", Database: "ebs_adapter"},
	{Name: "ebs_adapter_migrate", Database: "ebs_adapter", Migration: true},
	{Name: "ebs_adapter_runtime", Database: "ebs_adapter"},
	{Name: "gateway_auth_cleanup", Database: "gateway_auth"},
	{Name: "gateway_auth_migrate", Database: "gateway_auth", Migration: true},
	{Name: "gateway_auth_runtime", Database: "gateway_auth"},
	{Name: "identity_auth_migrate", Database: "identity_auth", Migration: true},
	{Name: "identity_auth_runtime", Database: "identity_auth"},
	{Name: "notification_chat_migrate", Database: "notification_chat", Migration: true},
	{Name: "notification_chat_runtime", Database: "notification_chat"},
	{Name: "wallet_ledger_webhook", Database: "wallet_ledger"},
	{Name: "wallet_ledger_migrate", Database: "wallet_ledger", Migration: true},
	{Name: "wallet_ledger_runtime", Database: "wallet_ledger"},
	{Name: "wallet_ledger_worker", Database: "wallet_ledger"},
	{Name: "workload_auth_cleanup", Database: "workload_auth"},
	{Name: "workload_auth_migrate", Database: "workload_auth", Migration: true},
	{Name: "workload_auth_runtime", Database: "workload_auth"},
}

func Roles() []Role {
	return append([]Role(nil), catalog...)
}

func MigrationRole(database string) (Role, bool) {
	for _, role := range catalog {
		if role.Database == database && role.Migration {
			return role, true
		}
	}
	return Role{}, false
}
