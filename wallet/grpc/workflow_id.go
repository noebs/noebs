package walletgrpc

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

func walletWorkflowID(domain, tenantID, commandKey string) string {
	material := make([]byte, 0, len(domain)+len(tenantID)+len(commandKey)+64)
	for _, value := range []string{"noebs.wallet.workflow.v1", domain, tenantID, commandKey} {
		material = binary.BigEndian.AppendUint64(material, uint64(len(value)))
		material = append(material, value...)
	}
	digest := sha256.Sum256(material)
	return fmt.Sprintf("wallet-%s-%x", domain, digest)
}
