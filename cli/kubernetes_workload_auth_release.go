package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/adonese/noebs/internal/workloadauth"
)

const workloadAuthDatabaseName = "workload_auth"

var workloadAuthCallerRoles = []serviceRole{
	serviceRoleAPIGateway,
	serviceRoleIdentityAuth,
	serviceRoleEBSAdapter,
}

type kubernetesReleaseWorkloadAuthInputs struct {
	Callers  map[string]kubernetesReleaseWorkloadCallerInput `yaml:"callers"`
	Database kubernetesReleaseWorkloadDatabaseInput          `yaml:"database"`
}

type kubernetesReleaseWorkloadCallerInput struct {
	KeyID      string `yaml:"key_id"`
	PrivateKey string `yaml:"private_key"`
}

type kubernetesReleaseWorkloadDatabaseInput struct {
	MigratePassword string `yaml:"migrate_password"`
	RuntimePassword string `yaml:"runtime_password"`
	CleanupPassword string `yaml:"cleanup_password"`
}

type preparedWorkloadAuthRelease struct {
	callers  map[serviceRole]preparedWorkloadCaller
	database preparedWorkloadDatabase
}

type preparedWorkloadCaller struct {
	keyID      string
	privateKey string
	publicKey  string
}

type preparedWorkloadDatabase struct {
	migratePassword string
	runtimePassword string
	cleanupPassword string
}

func requireExplicitWorkloadAuthInputs(inputs kubernetesReleaseWorkloadAuthInputs) error {
	for _, role := range workloadAuthCallerRoles {
		caller, ok := inputs.Callers[string(role)]
		if !ok || strings.TrimSpace(caller.KeyID) == "" || strings.TrimSpace(caller.PrivateKey) == "" {
			return fmt.Errorf("kubernetes release inputs require workload_auth.callers.%s key_id and private_key", role)
		}
		if caller.KeyID != strings.TrimSpace(caller.KeyID) || caller.PrivateKey != strings.TrimSpace(caller.PrivateKey) {
			return fmt.Errorf("kubernetes release inputs require canonical workload_auth.callers.%s authority", role)
		}
	}
	for label, value := range map[string]string{
		"migrate_password": inputs.Database.MigratePassword,
		"runtime_password": inputs.Database.RuntimePassword,
		"cleanup_password": inputs.Database.CleanupPassword,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("kubernetes release inputs require workload_auth.database.%s", label)
		}
		if value != strings.TrimSpace(value) {
			return fmt.Errorf("kubernetes release inputs require canonical workload_auth.database.%s", label)
		}
	}
	return nil
}

func prepareWorkloadAuthRelease(inputs kubernetesReleaseWorkloadAuthInputs, random io.Reader) (preparedWorkloadAuthRelease, error) {
	if random == nil {
		return preparedWorkloadAuthRelease{}, errors.New("workload authentication random source is required")
	}
	expected := make(map[string]bool, len(workloadAuthCallerRoles))
	for _, role := range workloadAuthCallerRoles {
		expected[string(role)] = true
	}
	for caller := range inputs.Callers {
		if !expected[caller] {
			return preparedWorkloadAuthRelease{}, fmt.Errorf("unsupported workload authentication caller %q", caller)
		}
	}

	prepared := preparedWorkloadAuthRelease{callers: make(map[serviceRole]preparedWorkloadCaller, len(workloadAuthCallerRoles))}
	seenKeyIDs := make(map[string]bool, len(workloadAuthCallerRoles))
	for _, role := range workloadAuthCallerRoles {
		caller, err := prepareWorkloadCaller(role, inputs.Callers[string(role)], random)
		if err != nil {
			return preparedWorkloadAuthRelease{}, err
		}
		if seenKeyIDs[caller.keyID] {
			return preparedWorkloadAuthRelease{}, fmt.Errorf("duplicate workload authentication key_id %q", caller.keyID)
		}
		seenKeyIDs[caller.keyID] = true
		prepared.callers[role] = caller
	}

	var err error
	prepared.database.migratePassword, err = prepareWorkloadDatabasePassword("migrate_password", inputs.Database.MigratePassword, random)
	if err != nil {
		return preparedWorkloadAuthRelease{}, err
	}
	prepared.database.runtimePassword, err = prepareWorkloadDatabasePassword("runtime_password", inputs.Database.RuntimePassword, random)
	if err != nil {
		return preparedWorkloadAuthRelease{}, err
	}
	prepared.database.cleanupPassword, err = prepareWorkloadDatabasePassword("cleanup_password", inputs.Database.CleanupPassword, random)
	if err != nil {
		return preparedWorkloadAuthRelease{}, err
	}
	if prepared.database.migratePassword == prepared.database.runtimePassword ||
		prepared.database.migratePassword == prepared.database.cleanupPassword ||
		prepared.database.runtimePassword == prepared.database.cleanupPassword {
		return preparedWorkloadAuthRelease{}, errors.New("workload authentication database passwords must be distinct")
	}
	return prepared, nil
}

func prepareWorkloadCaller(role serviceRole, input kubernetesReleaseWorkloadCallerInput, random io.Reader) (preparedWorkloadCaller, error) {
	keyID := strings.TrimSpace(input.KeyID)
	privateKey := strings.TrimSpace(input.PrivateKey)
	if (keyID == "") != (privateKey == "") {
		return preparedWorkloadCaller{}, fmt.Errorf("workload authentication caller %s requires both key_id and private_key", role)
	}
	if keyID == "" {
		var suffix [8]byte
		if _, err := io.ReadFull(random, suffix[:]); err != nil {
			return preparedWorkloadCaller{}, fmt.Errorf("generate workload authentication key id for %s: %w", role, err)
		}
		_, generated, err := ed25519.GenerateKey(random)
		if err != nil {
			return preparedWorkloadCaller{}, fmt.Errorf("generate workload authentication key for %s: %w", role, err)
		}
		keyID = string(role) + "-" + hex.EncodeToString(suffix[:])
		privateKey = base64.StdEncoding.EncodeToString(generated)
	}

	config := workloadauth.Config{SigningKeyID: keyID, SigningPrivateKey: privateKey}
	_, decoded, present, err := config.SigningKey()
	if err != nil || !present {
		return preparedWorkloadCaller{}, fmt.Errorf("workload authentication caller %s: %w", role, workloadauth.ErrInvalidConfiguration)
	}
	publicKey, ok := decoded.Public().(ed25519.PublicKey)
	if !ok {
		return preparedWorkloadCaller{}, fmt.Errorf("workload authentication caller %s has invalid public key", role)
	}
	return preparedWorkloadCaller{
		keyID:      keyID,
		privateKey: privateKey,
		publicKey:  base64.StdEncoding.EncodeToString(publicKey),
	}, nil
}

func prepareWorkloadDatabasePassword(label, input string, random io.Reader) (string, error) {
	password := strings.TrimSpace(input)
	if password == "" {
		var raw [32]byte
		if _, err := io.ReadFull(random, raw[:]); err != nil {
			return "", fmt.Errorf("generate workload authentication %s: %w", label, err)
		}
		return base64.RawURLEncoding.EncodeToString(raw[:]), nil
	}
	if len(password) < 24 {
		return "", fmt.Errorf("workload authentication database %s must contain at least 24 characters", label)
	}
	if strings.Contains(password, "REPLACE_WITH_") || strings.ContainsAny(password, "\r\n\x00") {
		return "", fmt.Errorf("workload authentication database %s is invalid", label)
	}
	return password, nil
}

func (r preparedWorkloadAuthRelease) configForRole(role serviceRole) map[string]interface{} {
	config := map[string]interface{}{}
	if caller, ok := r.callers[role]; ok {
		config["signing_key_id"] = caller.keyID
		config["signing_private_key"] = caller.privateKey
	}
	if roleReceivesSignedHTTP(role) {
		trusted := map[string]interface{}{}
		for callerRole := range expectedWorkloadCallers(role) {
			caller := r.callers[serviceRole(callerRole)]
			trusted[caller.keyID] = map[string]interface{}{
				"caller":     callerRole,
				"public_key": caller.publicKey,
			}
		}
		config["trusted_keys"] = trusted
		config["nonce_db_url"] = workloadAuthDatabaseURL("workload_auth_runtime", r.database.runtimePassword)
	}
	return config
}

func workloadAuthDatabaseURL(username, password string) string {
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(username, password),
		Host:     "postgres:5432",
		Path:     "/" + workloadAuthDatabaseName,
		RawQuery: "sslmode=verify-full",
	}).String()
}

func (r preparedWorkloadAuthRelease) databaseCredentialSecret() map[string]interface{} {
	return map[string]interface{}{
		"migrate_password": r.database.migratePassword,
		"runtime_password": r.database.runtimePassword,
		"cleanup_password": r.database.cleanupPassword,
	}
}
