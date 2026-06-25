package service

import (
	"path/filepath"
	"testing"

	"github.com/basketikun/infinite-canvas/config"
)

func TestGeneratedImageTaskMediaPathUsesAbsoluteSQLiteDataDir(t *testing.T) {
	previous := config.Cfg
	t.Cleanup(func() { config.Cfg = previous })
	root := t.TempDir()
	config.Cfg = config.Config{StorageDriver: "sqlite", DatabaseDSN: filepath.Join(root, "infinite-canvas.db")}

	if got := GeneratedImageTaskMediaPath("task.png"); got != filepath.Join(root, "generated-image-tasks", "task.png") {
		t.Fatalf("GeneratedImageTaskMediaPath = %q", got)
	}
}

func TestNormalizeGeneratedImageTaskMimeType(t *testing.T) {
	body := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

	if got := normalizeGeneratedImageTaskMimeType("", "https://example.com/file.png?x=1", body, ""); got != "image/png" {
		t.Fatalf("normalizeGeneratedImageTaskMimeType = %q", got)
	}
	if got := normalizeGeneratedImageTaskMimeType("image/jpg; charset=utf-8", "", nil, ""); got != "image/jpeg" {
		t.Fatalf("normalizeGeneratedImageTaskMimeType alias = %q", got)
	}
	if got := normalizeGeneratedImageTaskMimeType("", "https://example.com/file.jpg", nil, "image/png"); got != "image/jpeg" {
		t.Fatalf("normalizeGeneratedImageTaskMimeType fallback = %q", got)
	}
}

func TestDecodeImageTaskDataURLAcceptsUppercaseBase64(t *testing.T) {
	body, mimeType, err := decodeImageTaskDataURL("data:image/png;BASE64,AA==")
	if err != nil {
		t.Fatal(err)
	}
	if mimeType != "image/png" || len(body) != 1 {
		t.Fatalf("decodeImageTaskDataURL = mime %q bytes %d", mimeType, len(body))
	}
}
