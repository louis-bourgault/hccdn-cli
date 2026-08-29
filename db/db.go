package db

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/louis-bourgault/hccdn-cli/types"
	_ "github.com/ncruces/go-sqlite3/driver"
)

//go:embed init.sql
var initDBScript string

type DB struct {
	conn *sql.DB
	path string
}

var (
	instance *DB
	once     sync.Once
	openErr  error
)

func GetDB() (*DB, error) {
	once.Do(func() {
		path, err := defaultPath()
		if err != nil {
			openErr = err
			return
		}
		instance, openErr = Open(path)
	})
	return instance, openErr
}

func defaultPath() (string, error) {
	if override := os.Getenv("HCCDN_DB_PATH"); override != "" {
		return override, nil
	}
	var baseDir string
	var err error
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		baseDir, err = os.UserConfigDir()
	} else if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		baseDir = xdg
	} else {
		var home string
		home, err = os.UserHomeDir()
		baseDir = filepath.Join(home, ".local", "share")
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "hccdn-cli", "hccdn.db"), nil
}

// Open opens a database and migrates legacy schemas in place. It is exported so
// callers and tests can use an isolated database.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	db := &DB{conn: conn, path: path}
	if _, err = conn.Exec("PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;"); err != nil {
		conn.Close()
		return nil, err
	}
	if err = db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	if err = os.Chmod(path, 0o600); err != nil {
		conn.Close()
		return nil, fmt.Errorf("secure database permissions: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error { return db.conn.Close() }
func (db *DB) Path() string { return db.path }

func (db *DB) migrate() error {
	var version int
	if err := db.conn.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	var uploadsExists bool
	if err := db.conn.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND lower(name)='uploads')").Scan(&uploadsExists); err != nil {
		return err
	}
	if !uploadsExists {
		_, err := db.conn.Exec(initDBScript)
		return err
	}
	if version >= 2 {
		return nil
	}
	if err := db.backupLegacy(); err != nil {
		return fmt.Errorf("back up legacy database: %w", err)
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	columns := []struct{ table, name, definition string }{
		{"sessions", "completed_at", "TEXT"},
		{"sessions", "status", "TEXT NOT NULL DEFAULT 'complete'"},
		{"uploads", "source_sha256", "TEXT"},
		{"uploads", "payload_sha256", "TEXT"},
		{"uploads", "variant_key", "TEXT"},
		{"uploads", "deleted_at", "TEXT"},
	}
	for _, column := range columns {
		exists, err := columnExists(tx, column.table, column.name)
		if err != nil {
			return err
		}
		if !exists {
			query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", column.table, column.name, column.definition)
			if _, err := tx.Exec(query); err != nil {
				return err
			}
		}
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS session_items (
			session_id TEXT NOT NULL, upload_id TEXT NOT NULL, source_path TEXT NOT NULL,
			reused INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			removed_at TEXT, PRIMARY KEY (session_id, upload_id, source_path),
			FOREIGN KEY (session_id) REFERENCES sessions(id), FOREIGN KEY (upload_id) REFERENCES uploads(id))`,
		`CREATE TABLE IF NOT EXISTS file_cache (
			path TEXT PRIMARY KEY, size BIGINT NOT NULL, mod_time_ns BIGINT NOT NULL,
			sha256 TEXT NOT NULL, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT OR IGNORE INTO session_items (session_id, upload_id, source_path, reused)
			SELECT session_id, id, COALESCE(file, ''), 0 FROM uploads WHERE session_id IS NOT NULL`,
		`UPDATE sessions SET status = 'complete' WHERE status IS NULL OR status = ''`,
		`UPDATE sessions SET created_at = COALESCE(created_at,
			(SELECT MIN(created_at) FROM uploads WHERE uploads.session_id = sessions.id), CURRENT_TIMESTAMP)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uploads_cache_identity ON uploads(source_sha256, variant_key)
			WHERE source_sha256 IS NOT NULL AND variant_key IS NOT NULL AND deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS session_items_upload ON session_items(upload_id, removed_at)`,
		`CREATE INDEX IF NOT EXISTS session_items_path ON session_items(source_path, removed_at)`,
		`CREATE INDEX IF NOT EXISTS sessions_created ON sessions(created_at DESC)`,
		`PRAGMA user_version = 2`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) backupLegacy() error {
	backupPath := db.path + ".v1.bak"
	if _, err := os.Stat(backupPath); err == nil {
		return os.Chmod(backupPath, 0o600)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// VACUUM INTO creates a transactionally consistent snapshot, including data
	// that may have been present in a SQLite journal or WAL file.
	if _, err := db.conn.Exec("VACUUM INTO ?", backupPath); err != nil {
		return err
	}
	return os.Chmod(backupPath, 0o600)
}

func columnExists(tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (db *DB) BeginSession(commandText, fromDir string) (string, error) {
	id, err := genID()
	if err != nil {
		return "", err
	}
	_, err = db.conn.Exec(`INSERT INTO sessions (id, command_text, from_dir, status)
		VALUES (?, ?, ?, 'running')`, id, commandText, fromDir)
	return id, err
}

func genID() (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b), nil
}

func (db *DB) FinishSession(id, status string) error {
	_, err := db.conn.Exec(`UPDATE sessions SET status = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id)
	return err
}

func (db *DB) CachedHash(path string, size, modTimeNS int64) (string, bool, error) {
	var hash string
	err := db.conn.QueryRow(`SELECT sha256 FROM file_cache WHERE path = ? AND size = ? AND mod_time_ns = ?`, path, size, modTimeNS).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return hash, err == nil, err
}

func (db *DB) SaveCachedHash(path string, size, modTimeNS int64, hash string) error {
	_, err := db.conn.Exec(`INSERT INTO file_cache(path, size, mod_time_ns, sha256) VALUES (?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET size=excluded.size, mod_time_ns=excluded.mod_time_ns,
		sha256=excluded.sha256, updated_at=CURRENT_TIMESTAMP`, path, size, modTimeNS, hash)
	return err
}

const uploadColumns = `id, filename, size, url, content_type, COALESCE(created_at, ''),
	COALESCE(source_sha256, ''), COALESCE(payload_sha256, ''), COALESCE(variant_key, ''), COALESCE(file, '')`

func scanUpload(scanner interface{ Scan(...any) error }) (*types.Upload, error) {
	var upload types.Upload
	err := scanner.Scan(&upload.ID, &upload.Filename, &upload.Size, &upload.URL, &upload.ContentType,
		&upload.CreatedAt, &upload.SourceSHA256, &upload.PayloadSHA256, &upload.VariantKey, &upload.FileLoc)
	return &upload, err
}

func (db *DB) FindUpload(sourceHash, variantKey string) (*types.Upload, bool, error) {
	upload, err := scanUpload(db.conn.QueryRow(`SELECT `+uploadColumns+` FROM uploads
		WHERE source_sha256 = ? AND variant_key = ? AND deleted_at IS NULL LIMIT 1`, sourceHash, variantKey))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return upload, err == nil, err
}

func (db *DB) FindLegacyUpload(path, filename string) (*types.Upload, bool, error) {
	upload, err := scanUpload(db.conn.QueryRow(`SELECT `+uploadColumns+` FROM uploads
		WHERE file = ? AND filename = ? AND source_sha256 IS NULL AND deleted_at IS NULL
		ORDER BY rowid DESC LIMIT 1`, path, filename))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return upload, err == nil, err
}

func (db *DB) BackfillUpload(id, sourceHash, payloadHash, variantKey string) error {
	_, err := db.conn.Exec(`UPDATE uploads SET source_sha256=?, payload_sha256=?, variant_key=?
		WHERE id=? AND source_sha256 IS NULL`, sourceHash, payloadHash, variantKey, id)
	return err
}

func (db *DB) RecordUpload(upload *types.Upload, sessionID, sourcePath string, reused bool) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT OR IGNORE INTO files(path) VALUES (?)`, sourcePath); err != nil {
		return err
	}
	if !reused {
		_, err = tx.Exec(`INSERT INTO uploads
			(id, filename, size, session_id, url, content_type, created_at, file, source_sha256, payload_sha256, variant_key)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, upload.ID, upload.Filename, upload.Size, sessionID,
			upload.URL, upload.ContentType, upload.CreatedAt, sourcePath, upload.SourceSHA256, upload.PayloadSHA256, upload.VariantKey)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO session_items(session_id, upload_id, source_path, reused)
		VALUES (?, ?, ?, ?)`, sessionID, upload.ID, sourcePath, reused)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) History(limit int) ([]types.Session, error) {
	rows, err := db.conn.Query(`SELECT s.id, s.command_text, s.from_dir, COALESCE(s.created_at, ''),
		COALESCE(s.completed_at, ''), COALESCE(s.status, 'complete'),
		COALESCE(SUM(CASE WHEN i.reused=0 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN i.reused=1 THEN 1 ELSE 0 END),0), COUNT(i.upload_id)
		FROM sessions s LEFT JOIN session_items i ON i.session_id=s.id
		GROUP BY s.id ORDER BY s.created_at DESC, s.rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []types.Session
	for rows.Next() {
		var s types.Session
		if err := rows.Scan(&s.ID, &s.CommandText, &s.FromDir, &s.CreatedAt, &s.CompletedAt, &s.Status, &s.Uploaded, &s.Reused, &s.Total); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func (db *DB) Session(id string) (types.Session, bool, error) {
	var s types.Session
	err := db.conn.QueryRow(`SELECT s.id, s.command_text, s.from_dir, COALESCE(s.created_at, ''),
		COALESCE(s.completed_at, ''), COALESCE(s.status, 'complete'),
		COALESCE(SUM(CASE WHEN i.reused=0 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN i.reused=1 THEN 1 ELSE 0 END),0), COUNT(i.upload_id)
		FROM sessions s LEFT JOIN session_items i ON i.session_id=s.id WHERE s.id=? GROUP BY s.id`, id).
		Scan(&s.ID, &s.CommandText, &s.FromDir, &s.CreatedAt, &s.CompletedAt, &s.Status, &s.Uploaded, &s.Reused, &s.Total)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	return s, err == nil, err
}

func (db *DB) SessionUploads(id string) ([]types.Upload, error) {
	rows, err := db.conn.Query(`SELECT u.id, u.filename, u.size, u.url, u.content_type, COALESCE(u.created_at, ''),
		COALESCE(u.source_sha256, ''), COALESCE(u.payload_sha256, ''), COALESCE(u.variant_key, ''), i.source_path, i.reused,
		(i.removed_at IS NOT NULL) FROM session_items i
		JOIN uploads u ON u.id=i.upload_id WHERE i.session_id=? ORDER BY i.created_at, i.rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uploads []types.Upload
	for rows.Next() {
		var upload types.Upload
		if err := rows.Scan(&upload.ID, &upload.Filename, &upload.Size, &upload.URL, &upload.ContentType,
			&upload.CreatedAt, &upload.SourceSHA256, &upload.PayloadSHA256, &upload.VariantKey, &upload.FileLoc, &upload.Reused,
			&upload.Removed); err != nil {
			return nil, err
		}
		upload.Optimised = variantSetting(upload.VariantKey)
		uploads = append(uploads, upload)
	}
	return uploads, rows.Err()
}

func variantSetting(key string) string {
	if strings.HasPrefix(key, "original:") {
		return "none"
	}
	if strings.HasSuffix(key, ":full") {
		return "full"
	}
	if index := strings.LastIndex(key, "max="); index >= 0 {
		return key[index+4:]
	}
	return ""
}

type RemovalCandidate struct {
	Upload           types.Upload
	TargetReferences int
	OtherReferences  int
}

type Stats struct {
	Sessions         int `json:"sessions"`
	ActiveUploads    int `json:"active_uploads"`
	ActiveReferences int `json:"active_references"`
}

func (db *DB) Stats() (Stats, error) {
	var stats Stats
	err := db.conn.QueryRow(`SELECT
		(SELECT COUNT(*) FROM sessions),
		(SELECT COUNT(*) FROM uploads WHERE deleted_at IS NULL),
		(SELECT COUNT(*) FROM session_items WHERE removed_at IS NULL)`).
		Scan(&stats.Sessions, &stats.ActiveUploads, &stats.ActiveReferences)
	return stats, err
}

func (db *DB) removalCandidates(where string, args ...any) ([]RemovalCandidate, error) {
	query := `WITH target AS (SELECT upload_id, COUNT(*) n FROM session_items WHERE removed_at IS NULL AND ` + where + ` GROUP BY upload_id),
		allrefs AS (SELECT upload_id, COUNT(*) n FROM session_items WHERE removed_at IS NULL GROUP BY upload_id)
		SELECT ` + uploadColumns + `, target.n, allrefs.n-target.n FROM uploads u
		JOIN target ON target.upload_id=u.id JOIN allrefs ON allrefs.upload_id=u.id WHERE u.deleted_at IS NULL`
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []RemovalCandidate
	for rows.Next() {
		var c RemovalCandidate
		if err := rows.Scan(&c.Upload.ID, &c.Upload.Filename, &c.Upload.Size, &c.Upload.URL, &c.Upload.ContentType,
			&c.Upload.CreatedAt, &c.Upload.SourceSHA256, &c.Upload.PayloadSHA256, &c.Upload.VariantKey,
			&c.Upload.FileLoc, &c.TargetReferences, &c.OtherReferences); err != nil {
			return nil, err
		}
		candidates = append(candidates, c)
	}
	return candidates, rows.Err()
}

func (db *DB) RemovalCandidatesForSession(id string) ([]RemovalCandidate, error) {
	return db.removalCandidates("session_id = ?", id)
}

func (db *DB) RemovalCandidatesForPath(path string, directory bool) ([]RemovalCandidate, error) {
	if directory {
		prefix := strings.TrimSuffix(path, string(os.PathSeparator)) + string(os.PathSeparator) + "%"
		return db.removalCandidates("source_path LIKE ? ESCAPE '\\'", escapeLike(prefix))
	}
	return db.removalCandidates("source_path = ?", path)
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	// The final percent is the intentional wildcard for directory queries.
	return strings.TrimSuffix(value, `\%`) + `%`
}

func (db *DB) RemovalCandidatesAll() ([]RemovalCandidate, error) {
	return db.removalCandidates("1=1")
}

func (db *DB) RemoveSessionReference(sessionID, uploadID string, deleted bool) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE session_items SET removed_at=CURRENT_TIMESTAMP
		WHERE session_id=? AND upload_id=? AND removed_at IS NULL`, sessionID, uploadID); err != nil {
		return err
	}
	if deleted {
		if _, err = tx.Exec(`UPDATE uploads SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, uploadID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) RemovePathReferences(path string, directory bool, uploadID string, deleted bool) error {
	condition := "source_path = ?"
	arg := path
	if directory {
		condition = "source_path LIKE ? ESCAPE '\\'"
		arg = escapeLike(strings.TrimSuffix(path, string(os.PathSeparator)) + string(os.PathSeparator) + "%")
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE session_items SET removed_at=CURRENT_TIMESTAMP WHERE upload_id=? AND removed_at IS NULL AND `+condition, uploadID, arg); err != nil {
		return err
	}
	if deleted {
		if _, err = tx.Exec(`UPDATE uploads SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, uploadID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) RemoveAllReferences(uploadID string, deleted bool) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE session_items SET removed_at=CURRENT_TIMESTAMP WHERE upload_id=? AND removed_at IS NULL`, uploadID); err != nil {
		return err
	}
	if deleted {
		if _, err = tx.Exec(`UPDATE uploads SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, uploadID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) MarkSessionRemoved(id string) error {
	_, err := db.conn.Exec(`UPDATE sessions SET status='removed', completed_at=COALESCE(completed_at,CURRENT_TIMESTAMP) WHERE id=?`, id)
	return err
}

func (db *DB) MarkAllSessionsRemoved() error {
	_, err := db.conn.Exec(`UPDATE sessions SET status='removed', completed_at=COALESCE(completed_at,CURRENT_TIMESTAMP) WHERE status != 'removed'`)
	return err
}
