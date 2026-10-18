package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewFileHandlerServesFilesAndCustomNotFoundPage(t *testing.T) {
	tempDir := t.TempDir()

	mustWriteFile(t, filepath.Join(tempDir, "index.html"), "<h1>index</h1>")
	mustWriteFile(t, filepath.Join(tempDir, "404.html"), "<h1>not found</h1>")

	handler := newFileHandler(tempDir, false, true, "404.html")

	rootRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRecorder := httptest.NewRecorder()
	handler.ServeHTTP(rootRecorder, rootRequest)

	if rootRecorder.Code != http.StatusOK {
		t.Fatalf("expected root request to return 200, got %d", rootRecorder.Code)
	}

	if body := rootRecorder.Body.String(); body != "<h1>index</h1>" {
		t.Fatalf("expected root body %q, got %q", "<h1>index</h1>", body)
	}

	missingRequest := httptest.NewRequest(http.MethodGet, "/missing", nil)
	missingRecorder := httptest.NewRecorder()
	handler.ServeHTTP(missingRecorder, missingRequest)

	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected missing request to return 404, got %d", missingRecorder.Code)
	}

	if body := missingRecorder.Body.String(); body != "<h1>not found</h1>" {
		t.Fatalf("expected missing body %q, got %q", "<h1>not found</h1>", body)
	}
}

func TestNewFileHandlerFallsBackToDefaultNotFoundWhenCustomPageIsMissing(t *testing.T) {
	handler := newFileHandler(t.TempDir(), false, true, "404.html")

	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected missing request to return 404, got %d", recorder.Code)
	}

	if body := recorder.Body.String(); body != "404 page not found\n" {
		t.Fatalf("expected default not found body, got %q", body)
	}
}

func TestValidateServerConfig(t *testing.T) {
	testCases := []struct {
		name        string
		enableHTTP  bool
		enableHTTPS bool
		httpsCert   string
		httpsKey    string
		wantErr     bool
	}{
		{
			name:        "http only",
			enableHTTP:  true,
			enableHTTPS: false,
		},
		{
			name:        "https only",
			enableHTTP:  false,
			enableHTTPS: true,
			httpsCert:   "server.crt",
			httpsKey:    "server.key",
		},
		{
			name:        "http and https",
			enableHTTP:  true,
			enableHTTPS: true,
			httpsCert:   "server.crt",
			httpsKey:    "server.key",
		},
		{
			name:        "no listeners enabled",
			enableHTTP:  false,
			enableHTTPS: false,
			wantErr:     true,
		},
		{
			name:        "missing cert",
			enableHTTP:  false,
			enableHTTPS: true,
			httpsKey:    "server.key",
			wantErr:     true,
		},
		{
			name:        "missing key",
			enableHTTP:  false,
			enableHTTPS: true,
			httpsCert:   "server.crt",
			wantErr:     true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateServerConfig(testCase.enableHTTP, testCase.enableHTTPS, testCase.httpsCert, testCase.httpsKey)
			if testCase.wantErr && err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("expected validation to pass, got %v", err)
			}
		})
	}
}

func TestNewFileHandlerDisabledNotFound(t *testing.T) {
	handler := newFileHandler(t.TempDir(), false, false, "404.html")

	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", recorder.Code)
	}
}

func TestNewFileHandlerNotFoundFileIsDirectory(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "404.html"), 0o755); err != nil {
		t.Fatal(err)
	}

	handler := newFileHandler(tempDir, false, true, "404.html")

	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", recorder.Code)
	}

	if body := recorder.Body.String(); body != "404 page not found\n" {
		t.Fatalf("expected default not found body, got %q", body)
	}
}

func TestStaticStatusResponseWriterOverridesStatus(t *testing.T) {
	recorder := httptest.NewRecorder()
	w := &staticStatusResponseWriter{
		ResponseWriter: recorder,
		statusCode:     http.StatusNotFound,
	}

	if _, err := w.Write([]byte("content")); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, recorder.Code)
	}

	if body := recorder.Body.String(); body != "content" {
		t.Fatalf("expected body %q, got %q", "content", body)
	}
}

func TestStaticStatusResponseWriterIgnoresDuplicateWriteHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	w := &staticStatusResponseWriter{
		ResponseWriter: recorder,
		statusCode:     http.StatusNotFound,
	}

	w.WriteHeader(http.StatusOK)
	w.WriteHeader(http.StatusInternalServerError)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, recorder.Code)
	}
}

func TestNewFileHandlerDirListingDisabled(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	handler := newFileHandler(tempDir, false, true, "404.html")

	request := httptest.NewRequest(http.MethodGet, "/subdir/", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for directory without index.html, got %d", recorder.Code)
	}
}

func TestNewFileHandlerDirListingEnabled(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	handler := newFileHandler(tempDir, true, true, "404.html")

	request := httptest.NewRequest(http.MethodGet, "/subdir/", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 for directory listing, got %d", recorder.Code)
	}
}

func TestNewFileHandlerDirWithIndexServedWhenListingDisabled(t *testing.T) {
	tempDir := t.TempDir()
	subdir := filepath.Join(tempDir, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(subdir, "index.html"), "<h1>subdir</h1>")

	handler := newFileHandler(tempDir, false, true, "404.html")

	request := httptest.NewRequest(http.MethodGet, "/subdir/", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 for directory with index.html, got %d", recorder.Code)
	}

	if body := recorder.Body.String(); body != "<h1>subdir</h1>" {
		t.Fatalf("expected body %q, got %q", "<h1>subdir</h1>", body)
	}
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestPulse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pulse"), []byte("a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := withPulse(newFileHandler(dir, false, true, "404.html"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/pulse", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != banner+" "+version+"\n" {
		t.Fatalf("/pulse = %d %q", rec.Code, rec.Body.String())
	}
}
