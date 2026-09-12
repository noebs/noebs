package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

const inventory = `{"vms":[{"vm_name":"noebs-test","image":"ubuntu@sha256:abc","allocated_cpus":4,"memory_capacity_bytes":8589934592,"disk_capacity_bytes":53687091200,"ssh_dest":"vm+noebs-test@vm.exe.xyz","region":"pdx","proxy_share":"private"}]}`

func testClient(t *testing.T, handler http.HandlerFunc) *client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &client{token: "test-token", endpoint: server.URL, http: server.Client()}
}

func data(t *testing.T) *schema.ResourceData {
	d := schema.TestResourceDataRaw(t, vmResource().Schema, map[string]interface{}{
		"name": "noebs-test", "image": "old-image", "cpus": 1, "memory_gib": 2, "disk_gib": 10, "private": false,
	})
	d.SetId("noebs-test")
	return d
}

func TestReadRefreshesAllManagedFields(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing authorization")
		}
		fmt.Fprint(w, inventory)
	})
	d := data(t)
	if diags := readVM(context.Background(), d, c); diags.HasError() {
		t.Fatal(diags)
	}
	for key, want := range map[string]interface{}{"cpus": 4, "memory_gib": 8, "disk_gib": 50, "private": true, "image": "ubuntu@sha256:abc", "ssh_destination": "vm+noebs-test@vm.exe.xyz"} {
		if got := d.Get(key); got != want {
			t.Errorf("%s: got %v, want %v", key, got, want)
		}
	}
}

func TestReadOnlyRemovesConfirmedMissingVM(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		missing    bool
	}{
		{"missing", `{"vms":[]}`, 200, true},
		{"unauthorized", "private server response", 401, false},
		{"failure", "failed", 422, false},
		{"malformed", `{}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) })
			d := data(t)
			diags := readVM(context.Background(), d, c)
			if tc.missing {
				if d.Id() != "" || diags.HasError() {
					t.Fatalf("missing VM: %s %v", d.Id(), diags)
				}
			} else {
				if d.Id() == "" || !diags.HasError() {
					t.Fatalf("read failure erased state: %s %v", d.Id(), diags)
				}
			}
		})
	}
}

func TestDeleteDoesNotSwallowFailureOrPrematureSuccess(t *testing.T) {
	for _, status := range []int{200, 422} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if strings.HasPrefix(string(body), "'rm'") {
					w.WriteHeader(status)
					return
				}
				fmt.Fprint(w, inventory)
			})
			d := data(t)
			if diags := deleteVM(context.Background(), d, c); !diags.HasError() || d.Id() == "" {
				t.Fatalf("deletion incorrectly accepted: %v", diags)
			}
		})
	}
}

func TestCreateDoesNotAdoptExistingVM(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { calls++; fmt.Fprint(w, inventory) })
	d := data(t)
	d.SetId("")
	if diags := createVM(context.Background(), d, c); !diags.HasError() || calls != 1 || d.Id() != "" {
		t.Fatalf("unexpected adoption: %v %d", diags, calls)
	}
}

func TestCreateRecordsVMWhenResponseIsLost(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `{"vms":[]}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.HasPrefix(string(body), "'new'") {
			w.WriteHeader(504)
			return
		}
		fmt.Fprint(w, inventory)
	})
	d := data(t)
	d.SetId("")
	if diags := createVM(context.Background(), d, c); !diags.HasError() || d.Id() != "noebs-test" {
		t.Fatalf("mutation recovery lost VM: %v", diags)
	}
}

func TestMissingTokenRejectedAndSchemaValid(t *testing.T) {
	t.Setenv("EXEDEV_TOKEN", "")
	p := provider()
	if err := p.InternalValidate(); err != nil {
		t.Fatal(err)
	}
	if _, diags := p.ConfigureContextFunc(context.Background(), nil); !diags.HasError() {
		t.Fatal("missing token accepted")
	}
}

func TestOnlyKnownDigestAbbreviationSuppressesDifference(t *testing.T) {
	compare := vmResource().Schema["image"].DiffSuppressFunc
	full := "ubuntu@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254"
	if !compare("image", "ubuntu@sha256:224a1869", full, nil) {
		t.Fatal("documented API abbreviation changed immutable image")
	}
	if !compare("image", "boldsoftware/exeuntu@sha256:ea6c3f5c", "ghcr.io/boldsoftware/exeuntu@sha256:ea6c3f5c65d4122408c504d6e6ea6e2afd4ac629557428e2bff1d3dcfe67beb2", nil) {
		t.Fatal("registry display normalization changed immutable image")
	}
	for _, other := range []string{"ubuntu:latest", "ubuntu@sha256:124a1869", "other@sha256:224a1869", "ubuntu@sha256:224a186"} {
		if compare("image", other, full, nil) {
			t.Errorf("suppressed different image %q", other)
		}
	}
}
