package main

import (
	"os"
	"strings"
	"testing"
)

// TestDockerComposeHTTPSDefaults asserts the shipped compose file enables
// HTTPS on :7788 with a persistent data mount and HTTPS healthcheck.
// This guards the production deploy path used on workspace hosts.
func TestDockerComposeHTTPSDefaults(t *testing.T) {
	raw, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	text := string(raw)

	checks := []struct {
		name string
		want string
	}{
		{"https flag in command", "-https"},
		{"listen addr", ":7788"},
		{"data dir flag", "/data"},
		{"host port map", "7788:7788"},
		{"bind-mount data", "./data:/data"},
		{"healthcheck https URL", "https://127.0.0.1:7788/api/health"},
		{"healthcheck skip verify", "--no-check-certificate"},
	}
	for _, c := range checks {
		if !strings.Contains(text, c.want) {
			t.Errorf("%s: docker-compose.yml missing %q", c.name, c.want)
		}
	}

	// Must not default to plain HTTP health probe (would fail under -https).
	if strings.Contains(text, "http://127.0.0.1:7788/api/health") &&
		!strings.Contains(text, "https://127.0.0.1:7788/api/health") {
		t.Error("healthcheck still uses plain HTTP only")
	}
}

// TestDockerfileHTTPSDefaults asserts runtime image defaults match compose.
func TestDockerfileHTTPSDefaults(t *testing.T) {
	raw, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		`-https`,
		`https://127.0.0.1:7788/api/health`,
		`--no-check-certificate`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}
}

// TestChineseOpsDocPresent ensures the operator runbook ships in-repo.
func TestChineseOpsDocPresent(t *testing.T) {
	raw, err := os.ReadFile("docs/docker-deploy-ops.zh-CN.md")
	if err != nil {
		t.Fatalf("read Chinese ops doc: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"Docker Compose",
		"7788",
		"HTTPS",
		"备份",
		"迁移",
		"/api/health",
		"/api/ca.crt",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("ops doc missing section keyword %q", want)
		}
	}
}
