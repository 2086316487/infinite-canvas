package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/basketikun/infinite-canvas/service"
)

func GeneratedImageTaskMedia(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" || id != filepath.Base(id) || strings.Contains(id, "..") {
		http.NotFound(w, r)
		return
	}
	mimeType := mimeTypeByReferenceMediaExt(filepath.Ext(id))
	if !strings.HasPrefix(mimeType, "image/") {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(service.GeneratedImageTaskMediaPath(id))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, id, info.ModTime(), file)
}
