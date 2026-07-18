package handler

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/adonese/noebs/internal/workloadauth"
)

func testEBSAdapterWorkloadSigners(t *testing.T) *workloadauth.SignerSet {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signers, err := workloadauth.NewSignerSet(workloadauth.Config{
		SigningKeyID:      "ebs-adapter-test",
		SigningPrivateKey: base64.StdEncoding.EncodeToString(privateKey),
	}, []string{"card-vault"})
	if err != nil {
		t.Fatal(err)
	}
	return signers
}
