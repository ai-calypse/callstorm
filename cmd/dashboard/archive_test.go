package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveRetrievesOnlySelectedRun(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"run-1.json": `{"profile":"demo"}`, "run-1-calls.jsonl": "evidence\n", "run-2.json": "other"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runs/{id}/archive.tar.gz", getArchive(dir))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/runs/run-1/archive.tar.gz", nil))
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
	gz, err := gzip.NewReader(rr.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	got := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = string(b)
	}
	if len(got) != 2 || got["run-1-calls.jsonl"] != "evidence\n" {
		t.Fatalf("wrong archive: %+v", got)
	}
	for _, id := range []string{"missing", "run-*", ".."} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.SetPathValue("id", id)
		getArchive(dir)(rr, req)
		if rr.Code != 404 {
			t.Errorf("id=%q status=%d", id, rr.Code)
		}
	}
}
