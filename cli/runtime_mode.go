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
	serviceRoleAPIGateway   serviceRole = "api-gateway"
	serviceRoleWalletLedger serviceRole = "wallet-ledger"
	serviceRoleWalletWorker serviceRole = "wallet-worker"
	serviceRoleMigrate      serviceRole = "migrate"
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
		serviceRoleWalletLedger,
		serviceRoleWalletWorker,
		serviceRoleMigrate:
		return role, nil
	default:
		return "", fmt.Errorf("%w: %q", errInvalidServiceRole, value)
	}
}

func (r serviceRole) startsHTTP() bool {
	return r == serviceRoleAPIGateway
}

func (r serviceRole) startsGRPC() bool {
	return r == serviceRoleWalletLedger
}

func (r serviceRole) startsWalletWorker() bool {
	return r == serviceRoleWalletWorker
}

func (r serviceRole) startsChat() bool {
	return false
}

func (r serviceRole) startsBackgroundJobs() bool {
	return false
}

func (r serviceRole) runsMigrations() bool {
	return r == serviceRoleMigrate
}
