package interop

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

func TestTransportRequiresEveryExplicitAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, outbound, inbound, listen string
		peers                           []string
	}{
		{name: "empty"},
		{name: "missing outbound", inbound: "http://100.100.1.5:4000", listen: "0.0.0.0:4002", peers: []string{"100.100.1.5"}},
		{name: "missing inbound", outbound: "http://100.100.1.5:4001", listen: "0.0.0.0:4002", peers: []string{"100.100.1.5"}},
		{name: "missing listen", outbound: "http://100.100.1.5:4001", inbound: "http://100.100.1.5:4000", peers: []string{"100.100.1.5"}},
		{name: "missing peers", outbound: "http://100.100.1.5:4001", inbound: "http://100.100.1.5:4000", listen: "0.0.0.0:4002"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewTransportConfig(tc.outbound, tc.inbound, tc.listen, tc.peers); !errors.Is(err, ErrTransportConfig) {
				t.Fatalf("got %v, want explicit transport error", err)
			}
		})
	}
	if _, err := NewWorker(t.Context(), nil, "tenant-test", "noebs", TransportConfig{}); !errors.Is(err, ErrTransportConfig) {
		t.Fatalf("worker accepted zero transport: %v", err)
	}
	if _, err := (&Worker{}).Start(t.Context()); !errors.Is(err, ErrTransportConfig) {
		t.Fatalf("worker start accepted zero transport: %v", err)
	}
}

func TestTransportRejectsInvalidOriginsAndPeerAuthority(t *testing.T) {
	for _, value := range []string{"", " http://sdk:4001", "http://sdk:4001/", "http://user:secret@sdk", "http://sdk?token=secret", "http://sdk?", "http://sdk#fragment", "ftp://sdk", "http://sdk:0", "http://sdk:65536", "http://sdk:"} {
		if _, err := NewTransportConfig(value, "https://sdk.example", "0.0.0.0:4002", []string{"100.100.1.5"}); !errors.Is(err, ErrTransportConfig) {
			t.Errorf("origin %q: got %v", value, err)
		}
	}
	for _, value := range []string{"", ":4002", "sdk:4002", "8.8.8.8:4002", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:04002"} {
		if _, err := NewTransportConfig("https://sdk.example", "https://sdk.example:4000", value, []string{"100.100.1.5"}); !errors.Is(err, ErrTransportConfig) {
			t.Errorf("listener %q: got %v", value, err)
		}
	}
	for _, peers := range [][]string{nil, {""}, {"100.64.0.0/10"}, {"0.0.0.0"}, {"sdk.example"}, {"8.8.8.8"}, {"100.100.1.5", "100.100.1.5"}, {"::ffff:100.100.1.5"}, {"fe80::1%eth0"}} {
		if _, err := NewTransportConfig("https://sdk.example", "https://sdk.example:4000", "0.0.0.0:4002", peers); !errors.Is(err, ErrTransportConfig) {
			t.Errorf("peers %v: got %v", peers, err)
		}
	}
}

func TestBackendAcceptsOnlyConfiguredSocketPeers(t *testing.T) {
	peers := []string{"100.100.1.5", "fd7a:115c:a1e0::5"}
	config, err := NewTransportConfig("http://100.100.1.5:4001", "http://100.100.1.5:4000", "0.0.0.0:4002", peers)
	if err != nil {
		t.Fatal(err)
	}
	peers[0] = "100.100.1.9"
	for _, tc := range []struct {
		peer   string
		status int
	}{
		{"100.100.1.5:30000", http.StatusNotFound},
		{"[fd7a:115c:a1e0::5]:30000", http.StatusNotFound},
		{"100.100.1.9:30000", http.StatusForbidden},
		{"127.0.0.1:30000", http.StatusForbidden},
		{"192.0.2.5:30000", http.StatusForbidden},
		{"malformed", http.StatusForbidden},
	} {
		r := httptest.NewRequest(http.MethodGet, "/not-a-backend-route", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", "100.100.1.5")
		r.Header.Set("Forwarded", "for=100.100.1.5")
		response := httptest.NewRecorder()
		(&Worker{transport: config}).Handler().ServeHTTP(response, r)
		if response.Code != tc.status {
			t.Errorf("peer %s: got %d, want %d", tc.peer, response.Code, tc.status)
		}
	}
}

func TestWorkerUsesConfiguredSDKOrigins(t *testing.T) {
	var outboundPath, inboundPath string
	outbound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outboundPath = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
	defer outbound.Close()
	inbound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inboundPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer inbound.Close()
	config, err := NewTransportConfig(outbound.URL, inbound.URL, "127.0.0.1:4002", []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{transport: config, client: outbound.Client(), FSPID: "noebs"}
	if _, _, err := w.sdk(t.Context(), http.MethodGet, "/transfers/outbound", nil); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	var prepare IncomingPrepare
	prepare.Protocol.Prepare.Expiration = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	payload, err := json.Marshal(prepare)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.replayReservation(t.Context(), &walletstore.InteropQuote{TransferID: id}, &walletstore.InteropTransfer{OriginalPrepare: payload}); err != nil {
		t.Fatal(err)
	}
	if outboundPath != "/transfers/outbound" || inboundPath != "/transfers/"+id.String() {
		t.Fatalf("requests reached wrong SDK endpoints: outbound=%q inbound=%q", outboundPath, inboundPath)
	}
}
