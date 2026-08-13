package deploy_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Value-free static checks on the Nomad jobspec / deployment contract.
// These run without Nomad credentials and must never require secret values.

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// test file lives in deploy/nomad/tests
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

func TestJobspecHasNoSecretLiterals(t *testing.T) {
	root := repoRoot(t)
	job := filepath.Join(root, "deploy", "nomad", "jobs", "ultracore.nomad.hcl")
	b, err := os.ReadFile(job)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	// Forbid envsubst-style secret placeholders and obvious credential assignments.
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`(?i)password\s*=\s*"[^$"]`),
		regexp.MustCompile(`(?i)postgres://[^"]+:[^"@]+@`),
		regexp.MustCompile(`\$\{DATABASE_URL\}`),
		regexp.MustCompile(`\$\{CORE_MASTER_KEY\}`),
		regexp.MustCompile(`\$\{CORE_ADMIN_TOKEN\}`),
		regexp.MustCompile(`(?i)BEGIN (RSA |OPENSSH )?PRIVATE KEY`),
	}
	for _, re := range forbidden {
		if re.MatchString(text) {
			t.Fatalf("jobspec matches forbidden pattern %s", re.String())
		}
	}
	if !strings.Contains(text, `nomadVar "nomad/jobs/ultracore"`) {
		t.Fatal("jobspec must load secrets via nomadVar nomad/jobs/ultracore")
	}
	if regexp.MustCompile(`image\s*=\s*"[^"]*:latest"`).MatchString(text) {
		t.Fatal("jobspec must not use :latest as image authority")
	}
	if !regexp.MustCompile(`@sha256:[0-9a-f]{64}`).MatchString(text) {
		t.Fatal("jobspec must pin image by sha256 digest")
	}
	if !strings.Contains(text, `provider = "nomad"`) {
		t.Fatal("expected nomad service provider")
	}
	if !strings.Contains(text, `path     = "/readyz"`) {
		t.Fatal("expected /readyz health checks")
	}
	if !strings.Contains(text, "core.fleet.clark.team") {
		t.Fatal("expected internal Traefik hostname")
	}
	if strings.Contains(text, "ultracore-image-load") {
		t.Fatal("retired ultracore-image-load must not appear in jobspec")
	}
}

func TestDeploymentContractPlan03(t *testing.T) {
	root := repoRoot(t)
	p := filepath.Join(root, "deploy", "nomad", "deployment.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("deployment.yaml must be JSON-compatible: %v", err)
	}
	if data["schema_version"] != float64(1) {
		t.Fatalf("schema_version: %v", data["schema_version"])
	}
	if data["project"] != "ultralogical" {
		t.Fatalf("project: %v", data["project"])
	}
	if data["owner"] != "aleks-clark" {
		t.Fatalf("owner: %v", data["owner"])
	}
	if data["repository"] != "https://github.com/aleksclark/ultralogical" {
		t.Fatalf("repository: %v", data["repository"])
	}
	if data["ref_policy"] != "signed-default-branch-commit" {
		t.Fatalf("ref_policy: %v", data["ref_policy"])
	}
	if data["namespace"] != "default" {
		t.Fatalf("namespace: %v", data["namespace"])
	}
	sets, ok := data["release_sets"].([]any)
	if !ok || len(sets) != 1 {
		t.Fatalf("release_sets: %v", data["release_sets"])
	}
	rs := sets[0].(map[string]any)
	if rs["name"] != "ultracore" {
		t.Fatalf("release name: %v", rs["name"])
	}
	if rs["env"] != "env/home.nomadvars.hcl" || rs["images"] != "images.lock.hcl" {
		t.Fatalf("env/images: %v %v", rs["env"], rs["images"])
	}
	if rs["rollout"] != "serial" || rs["prune"] != "explicit-only" {
		t.Fatalf("rollout/prune: %v %v", rs["rollout"], rs["prune"])
	}
	jobs := rs["jobs"].([]any)
	j0 := jobs[0].(map[string]any)
	if j0["id"] != "ultracore" || j0["spec"] != "jobs/ultracore.nomad.hcl" {
		t.Fatalf("job entry: %v", j0)
	}
	vpaths, _ := rs["variable_paths"].([]any)
	found := false
	for _, v := range vpaths {
		if v == "nomad/jobs/ultracore" {
			found = true
		}
	}
	if !found {
		t.Fatal("variable_paths must include nomad/jobs/ultracore")
	}
	// No secret value shapes.
	if regexp.MustCompile(`(?i)postgres://[^:]+:[^@]+@`).MatchString(string(b)) {
		t.Fatal("deployment.yaml appears to contain a DSN with credentials")
	}
}

func TestImagesLockDigestOnly(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "deploy", "nomad", "images.lock.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !regexp.MustCompile(`@sha256:[0-9a-f]{64}`).MatchString(text) {
		t.Fatal("images.lock.hcl must contain digest pin")
	}
	if regexp.MustCompile(`(?m)^\s*image_\w+\s*=\s*"[^"]*:latest"`).MatchString(text) {
		t.Fatal("images.lock.hcl must not use :latest as image authority")
	}
	// Pin must match jobspec digest.
	job, err := os.ReadFile(filepath.Join(root, "deploy", "nomad", "jobs", "ultracore.nomad.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`sha256:[0-9a-f]{64}`)
	lockDigests := re.FindAllString(text, -1)
	jobDigests := re.FindAllString(string(job), -1)
	if len(lockDigests) < 1 || len(jobDigests) < 1 {
		t.Fatal("expected digests in lock and job")
	}
	want := lockDigests[0]
	for _, d := range jobDigests {
		if d != want {
			t.Fatalf("job digest %s != lock %s", d, want)
		}
	}
}

func TestEnvOverlayNonSecret(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "deploy", "nomad", "env", "home.nomadvars.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if regexp.MustCompile(`(?i)(password|secret|token|private_key)\s*=`).MatchString(text) {
		t.Fatal("env overlay must not assign secret-like keys")
	}
	if !strings.Contains(text, "core.fleet.clark.team") {
		t.Fatal("expected non-secret hostname overlay")
	}
}

func TestCODEOWNERS(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".github", "CODEOWNERS"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "/deploy/nomad/") || !strings.Contains(text, "@aleksclark") {
		t.Fatal("CODEOWNERS must cover /deploy/nomad/ with @aleksclark")
	}
}

func TestDockerfileIsNonRootDistroless(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "distroless/static-debian12") {
		t.Fatal("expected distroless base")
	}
	if !strings.Contains(text, "USER nonroot:nonroot") {
		t.Fatal("expected nonroot user")
	}
	if !strings.Contains(text, "@sha256:") {
		t.Fatal("expected digest-pinned base image(s)")
	}
	if strings.Contains(text, "CGO_ENABLED=1") {
		t.Fatal("production image should be static (CGO_ENABLED=0)")
	}
}

func TestDockerignoreExists(t *testing.T) {
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, ".dockerignore")); err != nil {
		t.Fatal(err)
	}
}

func TestExpectedServicesFixture(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "deploy", "nomad", "tests", "expected-services.json"))
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	if data["job_id"] != "ultracore" {
		t.Fatalf("job_id: %v", data["job_id"])
	}
}
