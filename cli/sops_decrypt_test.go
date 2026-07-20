package main

import (
	"bytes"
	cryptoaes "crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
	"gopkg.in/yaml.v3"
)

func TestDecryptSopsFileUsesExplicitAgeKeyWithoutSOPSCLIEnvironment(t *testing.T) {
	tmp := t.TempDir()
	fakeSOPS := filepath.Join(tmp, "sops")
	if err := os.WriteFile(fakeSOPS, []byte("#!/bin/sh\nexit 42\n"), 0o700); err != nil {
		t.Fatalf("write fake sops: %v", err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate age identity: %v", err)
	}
	recipient := identity.Recipient().String()
	ageKeyFile := filepath.Join(tmp, "age-key.txt")
	if err := os.WriteFile(ageKeyFile, []byte("# public key: "+recipient+"\n"+identity.String()+"\n"), 0o600); err != nil {
		t.Fatalf("write age key: %v", err)
	}
	secretFile := filepath.Join(tmp, "secrets.yaml")
	if err := os.WriteFile(secretFile, encryptedTestSOPSYAML(t, recipient), 0o600); err != nil {
		t.Fatalf("write encrypted secrets: %v", err)
	}

	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SOPS_AGE_KEY_FILE", "/ambient/key.txt")
	t.Setenv("AMBIENT_SECRET", "must-not-leak")

	output, err := decryptSopsFile(secretFile, ageKeyFile)
	if err != nil {
		t.Fatalf("decryptSopsFile() error = %v", err)
	}
	if strings.Contains(string(output), "sops:") {
		t.Fatalf("decryptSopsFile output kept SOPS metadata:\n%s", output)
	}
	if strings.Contains(string(output), "must-not-leak") || strings.Contains(string(output), "/ambient/key.txt") {
		t.Fatalf("decryptSopsFile output leaked ambient environment:\n%s", output)
	}

	var decoded map[string]map[string]interface{}
	if err := yaml.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("unmarshal decrypted output: %v\n%s", err, output)
	}
	noebs := decoded["noebs"]
	if got := noebs["data_key"]; got != "secret-data-key" {
		t.Fatalf("data_key = %#v, want secret-data-key", got)
	}
	if got := noebs["retry_count"]; got != 3 {
		t.Fatalf("retry_count = %#v, want 3", got)
	}
	if got := noebs["empty_value"]; got != "" {
		t.Fatalf("empty_value = %#v, want empty string", got)
	}
}

func encryptedTestSOPSYAML(t *testing.T, recipient string) []byte {
	t.Helper()
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatalf("generate test SOPS data key: %v", err)
	}
	lastModified := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	secretDataKey := encryptTestSOPSValue(t, dataKey, "noebs:data_key:", []byte("secret-data-key"), "str")
	retryCount := encryptTestSOPSValue(t, dataKey, "noebs:retry_count:", []byte("3"), "int")
	hash := sha512.New()
	_, _ = hash.Write([]byte("secret-data-key"))
	_, _ = hash.Write([]byte("3"))
	mac := fmt.Sprintf("%X", hash.Sum(nil))
	encryptedMAC := encryptTestSOPSValue(t, dataKey, lastModified.Format(time.RFC3339), []byte(mac), "str")
	encryptedDataKey := encryptTestSOPSDataKey(t, recipient, dataKey)

	return []byte(fmt.Sprintf(`noebs:
    data_key: %s
    retry_count: %s
    empty_value: ""
sops:
    age:
        - recipient: %s
          enc: |-
%s
    lastmodified: '%s'
    mac: %s
    unencrypted_suffix: _unencrypted
    version: 3.10.2
`, secretDataKey, retryCount, recipient, indentBlock(encryptedDataKey, 12), lastModified.Format(time.RFC3339), encryptedMAC))
}

func encryptTestSOPSDataKey(t *testing.T, recipient string, dataKey []byte) string {
	t.Helper()
	parsedRecipient, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		t.Fatalf("parse test age recipient: %v", err)
	}
	var encrypted bytes.Buffer
	armored := armor.NewWriter(&encrypted)
	writer, err := age.Encrypt(armored, parsedRecipient)
	if err != nil {
		t.Fatalf("create test age writer: %v", err)
	}
	if _, err := writer.Write(dataKey); err != nil {
		t.Fatalf("encrypt test data key: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close test age writer: %v", err)
	}
	if err := armored.Close(); err != nil {
		t.Fatalf("close test armor writer: %v", err)
	}
	return strings.TrimRight(encrypted.String(), "\n")
}

func encryptTestSOPSValue(t *testing.T, dataKey []byte, additionalData string, plaintext []byte, dataType string) string {
	t.Helper()
	block, err := cryptoaes.NewCipher(dataKey)
	if err != nil {
		t.Fatalf("create AES cipher: %v", err)
	}
	iv := make([]byte, 32)
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("generate test SOPS iv: %v", err)
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
	if err != nil {
		t.Fatalf("create AES GCM: %v", err)
	}
	out := gcm.Seal(nil, iv, plaintext, []byte(additionalData))
	return fmt.Sprintf("ENC[AES256_GCM,data:%s,iv:%s,tag:%s,type:%s]",
		base64.StdEncoding.EncodeToString(out[:len(out)-cryptoaes.BlockSize]),
		base64.StdEncoding.EncodeToString(iv),
		base64.StdEncoding.EncodeToString(out[len(out)-cryptoaes.BlockSize:]),
		dataType)
}

func indentBlock(value string, spaces int) string {
	padding := strings.Repeat(" ", spaces)
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = padding + line
	}
	return strings.Join(lines, "\n")
}

func TestSopsPlainValueForDecryptedBytesNormalizesTypes(t *testing.T) {
	tests := []struct {
		name     string
		payload  []byte
		dataType string
		wantText string
		wantMAC  string
		wantTag  string
	}{
		{name: "string", payload: []byte("hello"), dataType: "str", wantText: "hello", wantMAC: "hello", wantTag: "!!str"},
		{name: "int", payload: []byte("003"), dataType: "int", wantText: "3", wantMAC: "3", wantTag: "!!int"},
		{name: "float", payload: []byte("3.1400"), dataType: "float", wantText: "3.14", wantMAC: "3.14", wantTag: "!!float"},
		{name: "true", payload: []byte("True"), dataType: "bool", wantText: "true", wantMAC: "True", wantTag: "!!bool"},
		{name: "false", payload: []byte("False"), dataType: "bool", wantText: "false", wantMAC: "False", wantTag: "!!bool"},
		{name: "bytes", payload: []byte("raw"), dataType: "bytes", wantText: base64.StdEncoding.EncodeToString([]byte("raw")), wantMAC: "raw", wantTag: "!!binary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sopsPlainValueForDecryptedBytes(tt.payload, tt.dataType)
			if err != nil {
				t.Fatalf("sopsPlainValueForDecryptedBytes() error = %v", err)
			}
			if got.Text != tt.wantText || got.MACText != tt.wantMAC || got.YAMLTag != tt.wantTag {
				t.Fatalf("value = %#v, want text=%q mac=%q tag=%q", got, tt.wantText, tt.wantMAC, tt.wantTag)
			}
		})
	}
}

func TestPlainSopsScalarValueRejectsUnsupportedType(t *testing.T) {
	_, err := plainSopsScalarValue(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: "2026-05-28T00:00:00Z"})
	if err == nil {
		t.Fatalf("plainSopsScalarValue() error = nil, want unsupported type error")
	}
	if strings.Contains(err.Error(), "2026-05-28T00:00:00Z") {
		t.Fatalf("plainSopsScalarValue() error leaked value: %v", err)
	}
}
