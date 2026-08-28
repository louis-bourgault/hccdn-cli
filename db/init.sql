PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS files (
    path TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    command_text TEXT NOT NULL DEFAULT '',
    from_dir TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TEXT,
    status TEXT NOT NULL DEFAULT 'running'
);

CREATE TABLE IF NOT EXISTS uploads (
    id TEXT PRIMARY KEY,
    filename TEXT NOT NULL,
    size BIGINT NOT NULL,
    session_id TEXT,
    url TEXT NOT NULL,
    content_type TEXT,
    created_at TEXT,
    file TEXT,
    source_sha256 TEXT,
    payload_sha256 TEXT,
    variant_key TEXT,
    deleted_at TEXT,
    CONSTRAINT fk_upload_session FOREIGN KEY (session_id) REFERENCES sessions(id),
    CONSTRAINT fk_file FOREIGN KEY (file) REFERENCES files(path)
);

CREATE TABLE IF NOT EXISTS session_items (
    session_id TEXT NOT NULL,
    upload_id TEXT NOT NULL,
    source_path TEXT NOT NULL,
    reused INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    removed_at TEXT,
    PRIMARY KEY (session_id, upload_id, source_path),
    FOREIGN KEY (session_id) REFERENCES sessions(id),
    FOREIGN KEY (upload_id) REFERENCES uploads(id)
);

CREATE TABLE IF NOT EXISTS file_cache (
    path TEXT PRIMARY KEY,
    size BIGINT NOT NULL,
    mod_time_ns BIGINT NOT NULL,
    sha256 TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS uploads_cache_identity
    ON uploads(source_sha256, variant_key)
    WHERE source_sha256 IS NOT NULL AND variant_key IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS session_items_upload ON session_items(upload_id, removed_at);
CREATE INDEX IF NOT EXISTS session_items_path ON session_items(source_path, removed_at);
CREATE INDEX IF NOT EXISTS sessions_created ON sessions(created_at DESC);

PRAGMA user_version = 2;
