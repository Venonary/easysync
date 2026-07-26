package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContentTypeForFilenameMobileSafe(t *testing.T) {
	cases := map[string]string{
		"app.apk":   "application/vnd.android.package-archive",
		"cfg.toml":  "application/toml",
		"note.md":   "text/markdown; charset=utf-8",
		"photo.png": "image/png",
		"pack.zip":  "application/zip",
		"data.bin":  "application/octet-stream",
	}
	for name, want := range cases {
		if got := contentTypeForFilename(name); got != want {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
}

func TestContentDispositionAttachmentKeepsName(t *testing.T) {
	cd := contentDispositionAttachment("demo.apk")
	if !strings.Contains(cd, `filename="demo.apk"`) {
		t.Fatalf("missing ascii filename: %s", cd)
	}
	if !strings.Contains(cd, "filename*=UTF-8''demo.apk") {
		t.Fatalf("missing RFC5987 filename*: %s", cd)
	}
	if !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("expected attachment disposition: %s", cd)
	}
}

func TestServeUploadSetsDispositionAndAPKType(t *testing.T) {
	dir := t.TempDir()
	uploadDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Minimal ZIP local-file header so naive sniffers would call it zip;
	// our handler must still report APK type from the original name.
	stored := "aabbccdd.apk"
	payload := []byte("PK\x03\x04fake-apk-body")
	if err := os.WriteFile(filepath.Join(uploadDir, stored), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv := store.ListConversations()[0]
	_, err = store.AddMessage(Message{
		ConversationID: conv.ID,
		DeviceID:       "d1",
		Type:           "file",
		Content:        "my-app.apk",
		Attachment: &Attachment{
			URL:        "/uploads/" + stored,
			Name:       "my-app.apk",
			StoredName: stored,
			MimeType:   "application/zip", // wrong stored type; handler must override
			Size:       int64(len(payload)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := serveUploadHandler(dir, store)
	req := httptest.NewRequest(http.MethodGet, "/uploads/"+stored, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if ct != "application/vnd.android.package-archive" {
		t.Fatalf("Content-Type=%q, want APK type (not zip)", ct)
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "my-app.apk") {
		t.Fatalf("Content-Disposition missing original name: %s", cd)
	}
	if !strings.Contains(cd, "attachment") {
		t.Fatalf("expected attachment disposition: %s", cd)
	}
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("expected nosniff")
	}
	if got := rr.Body.Bytes(); string(got) != string(payload) {
		t.Fatalf("body mismatch")
	}
}

func TestServeUploadTOMLNotTextPlainOnly(t *testing.T) {
	dir := t.TempDir()
	uploadDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stored := "eeff0011.toml"
	payload := []byte("title = \"demo\"\n")
	if err := os.WriteFile(filepath.Join(uploadDir, stored), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv := store.ListConversations()[0]
	_, err = store.AddMessage(Message{
		ConversationID: conv.ID,
		DeviceID:       "d1",
		Type:           "file",
		Content:        "config.toml",
		Attachment: &Attachment{
			URL:        "/uploads/" + stored,
			Name:       "config.toml",
			StoredName: stored,
			MimeType:   "text/plain",
			Size:       int64(len(payload)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := serveUploadHandler(dir, store)
	req := httptest.NewRequest(http.MethodGet, "/uploads/"+stored, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if ct != "application/toml" {
		t.Fatalf("Content-Type=%q want application/toml", ct)
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "config.toml") {
		t.Fatalf("disposition %s", cd)
	}
}
