package main

import "testing"

func setServiceRoleForTest(t *testing.T, role serviceRole) {
	t.Helper()
	previousRole := noebsConfig.ServiceRole
	noebsConfig.ServiceRole = string(role)
	t.Cleanup(func() {
		noebsConfig.ServiceRole = previousRole
	})
}
