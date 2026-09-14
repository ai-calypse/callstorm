package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

// getArchive exports the source evidence, not a rendered dashboard. The report
// and optional companion files retain their names for later indexing/ingestion.
func getArchive(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		report, err := resolve(dir, id, ".json")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		root, err := os.OpenRoot(filepath.Dir(report))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer root.Close()
		var files []*os.File
		defer func() {
			for _, f := range files {
				f.Close()
			}
		}()
		for _, suffix := range []string{".json", ".csv", ".svg", "-calls.jsonl", "-insights.json"} {
			f, err := root.Open(id + suffix)
			if os.IsNotExist(err) && suffix != ".json" {
				continue
			}
			if err != nil {
				http.Error(w, "cannot read run artifacts", http.StatusInternalServerError)
				return
			}
			files = append(files, f)
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": id + ".tar.gz"}))
		gz := gzip.NewWriter(w)
		tw := tar.NewWriter(gz)
		defer gz.Close()
		defer tw.Close()
		for _, f := range files {
			st, err := f.Stat()
			if err != nil || !st.Mode().IsRegular() {
				log.Printf("archive: invalid artifact %s", f.Name())
				return
			}
			header := &tar.Header{Name: filepath.Base(f.Name()), Mode: 0644, Size: st.Size(), ModTime: st.ModTime()}
			if err := tw.WriteHeader(header); err != nil {
				return
			}
			if _, err := io.CopyN(tw, f, st.Size()); err != nil {
				log.Printf("archive: %v", err)
				return
			}
		}
	}
}
