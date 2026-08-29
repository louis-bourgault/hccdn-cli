package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/louis-bourgault/hccdn-cli/types"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestOpenMigratesLegacyDatabaseLazily(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	legacySchema := `
		CREATE TABLE Files (path TEXT PRIMARY KEY);
		CREATE TABLE sessions (id TEXT PRIMARY KEY, command_text TEXT, from_dir TEXT, created_at timestamp);
		CREATE TABLE Uploads (
			id TEXT PRIMARY KEY, filename TEXT, size BIGINT, session_id TEXT, url TEXT,
			content_type text, created_at timestamp, file text,
			FOREIGN KEY (session_id) REFERENCES sessions(id), FOREIGN KEY (file) REFERENCES Files(path));
		INSERT INTO files(path) VALUES ('/photos/a.jpg');
		INSERT INTO sessions(id, command_text, from_dir) VALUES ('legacy01', 'up', '/photos');
		INSERT INTO uploads(id, filename, size, session_id, url, content_type, created_at, file)
			VALUES ('upload01', 'a.jpg', 3, 'legacy01', 'https://cdn.example/a.jpg', 'image/jpeg',
			'2026-01-01T00:00:00Z', '/photos/a.jpg');`
	if _, err := conn.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := os.Stat(path + ".v1.bak"); err != nil {
		t.Fatalf("legacy backup was not created: %v", err)
	}

	legacy, found, err := database.FindLegacyUpload("/photos/a.jpg", "a.jpg")
	if err != nil || !found {
		t.Fatalf("legacy upload not found: found=%v err=%v", found, err)
	}
	if legacy.SourceSHA256 != "" {
		t.Fatalf("migration hashed an untouched legacy row: %q", legacy.SourceSHA256)
	}
	history, err := database.History(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Total != 1 || history[0].Status != "complete" {
		t.Fatalf("unexpected migrated history: %+v", history)
	}
	if err := database.BackfillUpload("upload01", "source-hash", "payload-hash", "original:v1"); err != nil {
		t.Fatal(err)
	}
	got, found, err := database.FindUpload("source-hash", "original:v1")
	if err != nil || !found || got.ID != "upload01" {
		t.Fatalf("backfilled upload not reusable: %+v found=%v err=%v", got, found, err)
	}
}

func TestReferencesPreventPrematureDeletion(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "new.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	session1, _ := database.BeginSession("up one", "/tmp")
	session2, _ := database.BeginSession("up two", "/tmp")
	upload := &types.Upload{
		ID: "upload01", URL: "https://cdn.example/a", Filename: "a.jpg", Size: 12,
		SourceSHA256: "source", PayloadSHA256: "payload", VariantKey: "original:v1",
	}
	if err := database.RecordUpload(upload, session1, "/tmp/a.jpg", false); err != nil {
		t.Fatal(err)
	}
	if err := database.RecordUpload(upload, session2, "/tmp/a.jpg", true); err != nil {
		t.Fatal(err)
	}
	candidates, err := database.RemovalCandidatesForSession(session1)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].OtherReferences != 1 {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
	if err := database.RemoveSessionReference(session1, upload.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := database.FindUpload("source", "original:v1"); !found {
		t.Fatal("shared upload was marked deleted")
	}
	candidates, err = database.RemovalCandidatesForSession(session2)
	if err != nil || len(candidates) != 1 || candidates[0].OtherReferences != 0 {
		t.Fatalf("unexpected final candidate: %+v err=%v", candidates, err)
	}
	if err := database.RemoveSessionReference(session2, upload.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := database.FindUpload("source", "original:v1"); found {
		t.Fatal("unreferenced upload remains cacheable")
	}
}

func TestDirectoryRemovalUsesPathBoundary(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "paths.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	session, _ := database.BeginSession("up", "/")
	for i, path := range []string{"/photos/a.jpg", "/photos-old/b.jpg"} {
		upload := &types.Upload{ID: "upload0" + string(rune('1'+i)), URL: "https://cdn.example/x", Filename: "x", Size: 1,
			SourceSHA256: "source" + string(rune('1'+i)), PayloadSHA256: "payload", VariantKey: "original:v1"}
		if err := database.RecordUpload(upload, session, path, false); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err := database.RemovalCandidatesForPath("/photos", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Upload.ID != "upload01" {
		t.Fatalf("directory prefix crossed a path boundary: %+v", candidates)
	}
}
