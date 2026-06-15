PRAGMA foreign_keys = ON;

CREATE TABLE if not exists Files (
    path TEXT PRIMARY KEY,

);

CREATE TABLE History (
    id TEXT PRIMARY KEY,
    command_text TEXT,
    from_dir TEXT,
    created_at timestamp
);

CREATE TABLE Uploads (
    id TEXT PRIMARY KEY,
    filename TEXT,
    size BIGINT,
    session_id TEXT,
    url TEXT,
    content_type text
    CONSTRAINT fk_upload_history
        FOREIGN KEY (session_id)
        REFERENCES History(id)
);

