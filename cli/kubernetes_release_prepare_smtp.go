package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"

	"github.com/adonese/noebs/internal/keycloakadmin"
	"gopkg.in/yaml.v3"
)

const keycloakSMTPEgressArtifact = "platform/keycloak-smtp-egress.yaml"

// SMTP has already been validated at the release configuration boundary.
// Kubernetes NetworkPolicy cannot select DNS names. As with other external
// transports, a DNS endpoint permits its explicit port to all destinations;
// use an IP address when the policy must restrict the destination as well.
func keycloakSMTPEgressPolicy(smtp *keycloakadmin.SMTPConfig) map[string]any {
	egress := []any{}
	if smtp != nil {
		rule := map[string]any{
			"ports": []any{map[string]any{"protocol": "TCP", "port": smtp.Port}},
		}
		if address, err := netip.ParseAddr(smtp.Host); err == nil {
			address = address.Unmap()
			rule["to"] = []any{map[string]any{
				"ipBlock": map[string]any{"cidr": netip.PrefixFrom(address, address.BitLen()).String()},
			}}
		}
		egress = append(egress, rule)
	}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name": "keycloak-smtp-egress", "namespace": "noebs",
		},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "keycloak"}},
			"policyTypes": []any{"Egress"},
			// Keep an explicit empty policy when SMTP is disabled so applying
			// the next release removes any previously granted SMTP egress.
			"egress": egress,
		},
	}
}

func validateKeycloakSMTPEgressPolicy(root string, smtp *keycloakadmin.SMTPConfig) error {
	payload, err := os.ReadFile(filepath.Join(root, keycloakSMTPEgressArtifact))
	if err != nil {
		return fmt.Errorf("read Keycloak SMTP egress policy: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(payload))
	var policy map[string]any
	if err := decoder.Decode(&policy); err != nil {
		return fmt.Errorf("parse Keycloak SMTP egress policy: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("Keycloak SMTP egress policy must contain one YAML document")
	}
	if !reflect.DeepEqual(policy, keycloakSMTPEgressPolicy(smtp)) {
		return errors.New("Keycloak SMTP egress policy does not match the release SMTP endpoint")
	}
	return nil
}
