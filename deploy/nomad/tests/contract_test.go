package deploy_test

import (
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
	job := filepath.Join(root, "deploy", "nomad", "ultracore.nomad.hcl")
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
	if !strings.Contains(text, `provider = "nomad"`) {
		t.Fatal("expected nomad service provider")
	}
	if !strings.Contains(text, `path     = "/readyz"`) {
		t.Fatal("expected /readyz health checks")
	}
	if !strings.Contains(text, "core.fleet.clark.team") {
		t.Fatal("expected internal Traefik hostname")
	}
}

func TestDeploymentContractListsSecretKeysOnly(t *testing.T) {
	root := repoRoot(t)
	p := filepath.Join(root, "deploy", "nomad", "deployment.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, key := range []string{
		"database_url",
		"master_key",
		"admin_token",
		"admin_token_role",
		"admin_cursor_secret",
		"nomad/jobs/ultracore",
		"forbid_latest_as_authority",
	} {
		if !strings.Contains(text, key) {
			t.Fatalf("deployment.yaml missing %q", key)
		}
	}
	// No obvious secret value shapes.
	if regexp.MustCompile(`(?i)postgres://[^:]+:[^@]+@`).MatchString(text) {
		t.Fatal("deployment.yaml appears to contain a DSN with credentials")
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
