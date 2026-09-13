package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPostDeployImageRoleContractMatchesFleetRender(t *testing.T) {
	render, err := exec.Command("kustomize", "build", filepath.Join("..", "infra", "kubernetes", "overlays", "exe")).CombinedOutput()
	if err != nil {
		t.Fatalf("render fleet: %v\n%s", err, render)
	}
	var objects []map[string]interface{}
	decoder := yaml.NewDecoder(bytes.NewReader(render))
	for {
		var object map[string]interface{}
		if err := decoder.Decode(&object); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		if len(object) > 0 {
			objects = append(objects, object)
		}
	}
	payload, err := json.Marshal(objects)
	if err != nil {
		t.Fatal(err)
	}
	// The contract is compared with the real rendered overlay, independently of
	// the synthetic runtime fixtures used by the Python checker regression tests.
	program := `
import importlib.util,json,sys
spec=importlib.util.spec_from_file_location('checker',sys.argv[1])
checker=importlib.util.module_from_spec(spec);spec.loader.exec_module(checker)
actual_deployments={};actual_cronjobs=set();actual_jobs={}
for item in json.load(sys.stdin):
    kind=item.get('kind')
    if kind not in ('Deployment','CronJob','Job'):continue
    template=item['spec']['jobTemplate']['spec']['template'] if kind=='CronJob' else item['spec']['template']
    containers=template['spec']['containers']
    if not any(c['image'].startswith('ghcr.io/noebs/noebs@') for c in containers):continue
    names=sorted(c['name'] for c in containers)
    if kind=='Deployment':actual_deployments[item['metadata']['name']]=names
    elif kind=='CronJob':actual_cronjobs.add(item['metadata']['name'])
    else:actual_jobs[template['metadata']['labels'][checker.LABEL]]=(names,sorted(c['name'] for c in template['spec'].get('initContainers',[])))
assert actual_deployments=={name:sorted(roles) for name,roles in checker.DEPLOYMENTS.items()},actual_deployments
assert actual_cronjobs==set(checker.CRONJOBS),actual_cronjobs
assert actual_jobs=={name:tuple(map(sorted,roles)) for name,roles in checker.OPTIONAL_JOB_ROLES.items()},actual_jobs
`
	command := exec.Command("python3", "-B", "-c", program, filepath.Join("..", "scripts", "alpha-workload-images.py"))
	command.Stdin = bytes.NewReader(payload)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("smoke image role contract differs from fleet render: %v\n%s", err, output)
	}
}
