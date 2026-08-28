package webui

import (
	"bytes"
	"embed"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

//go:embed all:dist
var embedded embed.FS

const bootstrapIndex = `<!doctype html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>ConfigHub</title></head>
<body><div id="root">ConfigHub web assets have not been built.</div></body>
</html>
`

type fallbackFS struct {
	primary fs.FS
}

func (f fallbackFS) Open(name string) (fs.File, error) {
	file, err := f.primary.Open(name)
	if err == nil || !isNotExist(err) {
		return file, err
	}
	if name != "index.html" {
		return nil, err
	}
	return &bootstrapFile{Reader: bytes.NewReader([]byte(bootstrapIndex))}, nil
}

type bootstrapFile struct{ *bytes.Reader }

func (*bootstrapFile) Close() error               { return nil }
func (*bootstrapFile) Stat() (fs.FileInfo, error) { return bootstrapFileInfo{}, nil }

type bootstrapFileInfo struct{}

func (bootstrapFileInfo) Name() string       { return "index.html" }
func (bootstrapFileInfo) Size() int64        { return int64(len(bootstrapIndex)) }
func (bootstrapFileInfo) Mode() fs.FileMode  { return 0o444 }
func (bootstrapFileInfo) ModTime() time.Time { return time.Time{} }
func (bootstrapFileInfo) IsDir() bool        { return false }
func (bootstrapFileInfo) Sys() any           { return nil }

func Assets() (fs.FS, error) {
	assets, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, err
	}
	return fallbackFS{primary: assets}, nil
}

var hashedAssetName = regexp.MustCompile(`(?:^|/)[^/]+[-.][A-Za-z0-9_-]{8,}\.[^/]+$`)

// NewHandler serves an embedded asset filesystem with history fallback for
// extensionless SPA routes.
func NewHandler(assets fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		requestPath := path.Clean("/" + r.URL.Path)
		if requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(requestPath, "/")
		if name == "" {
			name = "index.html"
		}
		contents, err := fs.ReadFile(assets, name)
		if err != nil {
			if path.Ext(name) != "" || !isNotExist(err) {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
			contents, err = fs.ReadFile(assets, name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}

		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else if hashedAssetName.MatchString(name) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(contents))
	})
}

func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
