package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestKeycloakOperationRunnerUsesDeployedReleaseAuthority(t *testing.T) {
	const revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const image = "ghcr.io/noebs/noebs@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	render, err := exec.Command("kustomize", "build", filepath.Join("..", "infra", "kubernetes", "overlays", "exe")).CombinedOutput()
	if err != nil {
		t.Fatalf("render fleet: %v\n%s", err, render)
	}
	fixture := map[string]any{"noebs-release": map[string]any{"data": map[string]string{"revision": revision, "image": image, "stage": "production"}}}
	decoder := yaml.NewDecoder(bytes.NewReader(render))
	for {
		var object map[string]any
		if err := decoder.Decode(&object); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		if object["kind"] != "ConfigMap" {
			continue
		}
		name := object["metadata"].(map[string]interface{})["name"].(string)
		if strings.HasPrefix(name, "tenant-catalog-") || strings.HasPrefix(name, "keycloak-desired-state-") {
			fixture[name] = object
		}
	}
	fixturePayload, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mode, scenario string
		wantSuccess          bool
	}{
		{"lookup", "lookup", "valid", true},
		{"membership dry run", "dry-run", "valid", true},
		{"membership apply", "apply", "valid", true},
		{"different revision", "apply", "revision", false},
		{"different image authority", "apply", "image", false},
		{"changed live authority", "apply", "authority", false},
		{"unready Keycloak", "apply", "unready", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			write := func(name, payload string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(directory, name), []byte(payload), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("fixture.json", string(fixturePayload), 0600)
			write("git", "#!/bin/sh\ncase $3 in\n  diff|ls-files) exit 0 ;;\n  rev-parse) echo "+revision+" ;;\n  *) exit 99 ;;\nesac\n", 0700)
			write("kubectl", `#!/usr/bin/env python3
import json, os, pathlib, sys, yaml
root = pathlib.Path(os.environ['NOEBS_OPERATION_TEST_DIR'])
fixture = json.loads((root / 'fixture.json').read_text())
scenario = os.environ['NOEBS_OPERATION_TEST_SCENARIO']
args = sys.argv[1:]
if args[:2] == ['-n', 'noebs']:
    args = args[2:]
if args[:2] == ['get', 'configmap']:
    value = fixture[args[2]]
    if args[2] == 'noebs-release':
        if scenario == 'revision': value['data']['revision'] = 'c' * 40
        if scenario == 'image': value['data']['image'] = 'example.com/foreign@sha256:' + 'b' * 64
    elif scenario == 'authority':
        key = next(iter(value['data']))
        value['data'][key] += 'changed'
    print(json.dumps(value))
elif args[:3] == ['rollout', 'status', 'deployment/keycloak']:
    sys.exit(1 if scenario == 'unready' else 0)
elif args[:1] == ['apply']:
    value = yaml.safe_load(pathlib.Path(args[args.index('-f') + 1]).read_text())
    if value['kind'] != 'Job': raise SystemExit('operation attempted shared authority mutation')
    if '--dry-run=server' not in args:
        (root / 'applied.json').write_text(json.dumps(value))
    print(json.dumps(value))
else:
    raise SystemExit('unexpected kubectl request: ' + repr(args))
`, 0700)
			t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("NOEBS_OPERATION_TEST_DIR", directory)
			t.Setenv("NOEBS_OPERATION_TEST_SCENARIO", tc.scenario)
			command := exec.Command("bash", filepath.Join("..", "infra", "kubernetes", "operations", "run-keycloak-job.sh"), tc.mode)
			output, runErr := command.CombinedOutput()
			if (runErr == nil) != tc.wantSuccess {
				t.Fatalf("runner returned %v, want success=%t\n%s", runErr, tc.wantSuccess, output)
			}
			payload, err := os.ReadFile(filepath.Join(directory, "applied.json"))
			if !tc.wantSuccess {
				if !os.IsNotExist(err) {
					t.Fatalf("rejected operation applied a Job: %s (%v)", payload, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var applied map[string]any
			if err := json.Unmarshal(payload, &applied); err != nil {
				t.Fatal(err)
			}
			spec := applied["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
			for _, field := range []string{"containers", "initContainers"} {
				for _, value := range spec[field].([]any) {
					if got := value.(map[string]any)["image"]; got != image {
						t.Fatalf("%s image = %v, want deployed image %s", field, got, image)
					}
				}
			}
		})
	}
}
