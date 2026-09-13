package backofficeauth

import "testing"

func TestBackofficeOriginIsExplicitAndSeparate(t *testing.T) {
	const public = "https://api.noebs.sd"
	for _, origin := range []string{"https://noebs-workers.tail09832.ts.net", "https://operations.example"} {
		if err := ValidateSeparateOrigin(origin, public); err != nil {
			t.Fatalf("valid separate origin %q: %v", origin, err)
		}
	}
	for _, origin := range []string{"", public, "http://ops.example", "https://ops.example/", "https://ops.example?", "https://ops.example?q=x", "https://ops.example#fragment", "https://ops.example:443", "https://user@ops.example", "https://*.example", "https://OPS.example", "https://ops.example.", "https://100.85.107.107", "https://[fd7a:115c:a1e0::1]", "https://ops..example", "https://-ops.example"} {
		if err := ValidateSeparateOrigin(origin, public); err == nil {
			t.Errorf("accepted origin %q", origin)
		}
	}
	if err := ValidateSeparateOrigin("https://ops.example", ""); err == nil {
		t.Fatal("accepted missing public origin")
	}
}
