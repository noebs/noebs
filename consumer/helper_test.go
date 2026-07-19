package consumer

import (
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestIPINOperationNamesUseExplicitIPINEndpoint(t *testing.T) {
	service := Service{NoebsConfig: ebs_fields.NoebsConfig{
		ConsumerIP: "https://consumer.example/",
		IPINIp:     "https://ipin.example/",
	}}

	if got := service.ToDatabasename(service.NoebsConfig.IPINIp + ebs_fields.IPinGeneration); got != "generate_ipin" {
		t.Fatalf("explicit IPIN operation = %q, want generate_ipin", got)
	}
	if got := service.ToDatabasename(service.NoebsConfig.ConsumerIP + ebs_fields.IPinGeneration); got != "" {
		t.Fatalf("consumer-base IPIN operation = %q, want empty", got)
	}
}
