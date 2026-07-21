package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveUploadFilesDeletesStoredAttachments(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	uploadDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(uploadDir, "expired-photo.jpg")
	if err := os.WriteFile(target, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Path traversal and empty names must be ignored.
	removeUploadFiles(store, []string{"expired-photo.jpg", "", "../etc/passwd", "sub/nope"})
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected attachment file removed, stat err=%v", err)
	}
}

func TestDockerEntrypointFixesDataOwnership(t *testing.T) {
	script, err := os.ReadFile("docker-entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, needle := range []string{"chown -R easysync:easysync", "su-exec easysync", "EASYSYNC_DATA"} {
		if !contains(text, needle) {
			t.Fatalf("entrypoint missing %q", needle)
		}
	}
	dockerfile, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	df := string(dockerfile)
	for _, needle := range []string{"docker-entrypoint.sh", "su-exec", "ENTRYPOINT [\"/app/docker-entrypoint.sh\"]"} {
		if !contains(df, needle) {
			t.Fatalf("Dockerfile missing %q", needle)
		}
	}
	// Must not pin USER easysync before entrypoint (would block chown).
	if contains(df, "USER easysync") {
		t.Fatal("Dockerfile should not USER easysync; entrypoint drops privileges after chown")
	}
}
