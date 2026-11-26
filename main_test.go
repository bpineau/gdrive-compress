package main

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

func TestListImagesThresholdBeforeLimit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[{"id":"exact","size":"1024"},{"id":"larger","size":"1025"}]}`))
	}))
	defer s.Close()
	ctx := context.Background()
	srv, err := drive.NewService(ctx, option.WithEndpoint(s.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	files, err := listImages(ctx, srv, 1024, nil, 1)
	if err != nil || len(files) != 1 || files[0].ID != "larger" {
		t.Fatalf("files = %v, err = %v", files, err)
	}
}

func TestDumpDriveNameContainingSlash(t *testing.T) {
	dir := t.TempDir()
	if err := dumpPair(dir, imgFile{ID: "123", Name: "Summer/Beach.jpg"}, []byte("original"), []byte("comparison")); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"123_orig_Summer_Beach.jpg": "original", "123_new_Summer_Beach.jpg": "comparison"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(b) != want {
			t.Errorf("%s: got %q, err %v", name, b, err)
		}
	}
}

func TestResumeAfterInterruptedLogWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.jsonl")
	if err := os.WriteFile(path, []byte("{\"id\":\"first\"}\n{\"id\":"), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.write(logEntry{FileID: "next"}); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	done, err := loadProcessed(path)
	if err != nil || !done["first"] || !done["next"] {
		t.Fatalf("completed files = %v, err = %v", done, err)
	}
}

func TestCompressionAccounting(t *testing.T) {
	if _, err := exec.LookPath("magick"); err != nil {
		t.Skip("requires ImageMagick")
	}
	var src bytes.Buffer
	if err := jpeg.Encode(&src, image.NewGray(image.Rect(0, 0, 32, 32)), nil); err != nil {
		t.Fatal(err)
	}
	src.Write(make([]byte, 8192))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "upload refused", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(src.Bytes())
	}))
	defer server.Close()
	ctx := context.Background()
	srv, err := drive.NewService(ctx, option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	for _, apply := range []bool{false, true} {
		dir := t.TempDir()
		log, err := openLog(filepath.Join(dir, "progress.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		r := runner{srv: srv, plog: log, maxSize: 4096, apply: apply, dumpDir: dir}
		r.processOne(ctx, imgFile{ID: "test", Name: "sample.jpg", Size: int64(src.Len())})
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		if apply {
			if r.stats.errs.Load() != 1 || r.stats.bytesIn.Load() != 0 || r.stats.bytesOut.Load() != 0 || r.stats.done.Load() != 0 {
				t.Fatal("failed upload counted in savings")
			}
		} else {
			res, err := os.ReadFile(filepath.Join(dir, "test_new_sample.jpg"))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(res))
			if err != nil || cfg.Width != 32 || cfg.Height != 32 || r.stats.done.Load() != 1 || r.stats.errs.Load() != 0 || r.stats.bytesIn.Load() != int64(src.Len()) || r.stats.bytesOut.Load() != int64(len(res)) {
				t.Fatal("incorrect dry-run result", cfg, err)
			}
		}
	}
}
