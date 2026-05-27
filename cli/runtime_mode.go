package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type serviceRole string

var (
	errMissingServiceRole = errors.New("missing NOEBS_SERVICE")
	errInvalidServiceRole = errors.New("invalid NOEBS_SERVICE")
)

const (
	serviceRoleAPIGateway     serviceRole = "api-gateway"
	serviceRolePSPWebhook     serviceRole = "psp-webhook"
	serviceRoleAdminReporting serviceRole = "admin-reporting"
	serviceRoleNotification   serviceRole = "notification-chat"
	serviceRoleWalletLedger   serviceRole = "wallet-ledger"
	serviceRoleWalletWorker   serviceRole = "wallet-worker"
	serviceRoleMigrate        serviceRole = "migrate"
)

func currentServiceRole() (serviceRole, error) {
	return parseServiceRole(os.Getenv("NOEBS_SERVICE"))
}

func parseServiceRole(value string) (serviceRole, error) {
	role := serviceRole(strings.TrimSpace(value))
	switch role {
	case "":
		return "", errMissingServiceRole
	case serviceRoleAPIGateway,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleWalletLedger,
		serviceRoleWalletWorker,
		serviceRoleMigrate:
		return role, nil
	default:
		return "", fmt.Errorf("%w: %q", errInvalidServiceRole, value)
	}
}

func (r serviceRole) startsHTTP() bool {
	return r == serviceRoleAPIGateway || r == serviceRolePSPWebhook || r == serviceRoleAdminReporting || r == serviceRoleNotification
}

func (r serviceRole) startsGRPC() bool {
	return r == serviceRoleWalletLedger
}

func (r serviceRole) startsWalletWorker() bool {
	return r == serviceRoleWalletWorker
}

func (r serviceRole) startsChat() bool {
	return r == serviceRoleNotification
}

func (r serviceRole) startsBackgroundJobs() bool {
	return false
}

func (r serviceRole) runsMigrations() bool {
	return r == serviceRoleMigrate
}
