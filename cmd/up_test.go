package cmd

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestParseVariantsValidatesAndDeduplicates(t *testing.T) {
	command := &cobra.Command{}
	command.Flags().String("optimise", "", "")
	if err := command.Flags().Set("optimise", "none, full,720,720"); err != nil {
		t.Fatal(err)
	}
	variants, err := parseVariants(command)
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 3 || variants[2].key != "webp:v1:q85:max=720" {
		t.Fatalf("unexpected variants: %+v", variants)
	}
	if err := command.Flags().Set("optimise", "nope"); err != nil {
		t.Fatal(err)
	}
	if _, err := parseVariants(command); err == nil {
		t.Fatal("invalid optimisation was accepted")
	}
}

func TestRunUpReusesUnchangedFile(t *testing.T) {
	var uploads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload" {
			http.NotFound(w, r)
			return
		}
		id := uploads.Add(1)
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
		} else {
			file.Close()
		}
		fmt.Fprintf(w, `{"id":"up%d","filename":"note.txt","size":5,"content_type":"text/plain","url":"%s/file/up%d","created_at":"now"}`, id, serverURL(r), id)
	}))
	defer server.Close()
	t.Setenv("HCCDN_API_KEY", "secret")
	t.Setenv("HCCDN_API_URL", server.URL)
	t.Setenv("HCCDN_DB_PATH", filepath.Join(t.TempDir(), "cli.db"))
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	upCmd.SetOut(&output)
	upCmd.SetErr(io.Discard)
	if err := runUp(upCmd, []string{path}); err != nil {
		t.Fatal(err)
	}
	if err := runUp(upCmd, []string{path}); err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 1 {
		t.Fatalf("unchanged file uploaded %d times", uploads.Load())
	}
	moved := filepath.Join(t.TempDir(), "copy.txt")
	if err := os.WriteFile(moved, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runUp(upCmd, []string{moved}); err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 1 {
		t.Fatalf("identical content at another path was uploaded again")
	}
	if err := os.WriteFile(path, []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if err := runUp(upCmd, []string{path}); err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 2 {
		t.Fatalf("changed file was incorrectly reused; uploads=%d", uploads.Load())
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func TestCollectFilesRecursiveExcludesManifests(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"a.txt", "old.hccdn.json", filepath.Join("nested", "b.txt")} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	flat, err := collectFiles(root, true, false)
	if err != nil || len(flat) != 1 {
		t.Fatalf("unexpected flat files: %+v err=%v", flat, err)
	}
	recursive, err := collectFiles(root, true, true)
	if err != nil || len(recursive) != 2 {
		t.Fatalf("unexpected recursive files: %+v err=%v", recursive, err)
	}
}

func TestOptimisedPayloadAndOversizeFiltering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.PNG")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 20; x++ {
			canvas.Set(x, y, color.RGBA{R: uint8(x), A: 255})
		}
	}
	if err := png.Encode(file, canvas); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if !isOptimisable(path) {
		t.Fatal("uppercase image extension was not recognised")
	}
	payload, err := preparePayload(path, "source", "full")
	if err != nil || len(payload.data) == 0 || payload.hash == "" {
		t.Fatalf("optimisation failed: %+v err=%v", payload, err)
	}
	filtered, err := filterOversizedVariants(path, []variant{{setting: "10"}, {setting: "20"}, {setting: "full"}})
	if err != nil || len(filtered) != 2 || filtered[0].setting != "10" {
		t.Fatalf("unexpected filtering: %+v err=%v", filtered, err)
	}
}
