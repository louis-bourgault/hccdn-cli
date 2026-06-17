PRAGMA foreign_keys = ON;

CREATE TABLE if not exists Files (
    path TEXT PRIMARY KEY
);

CREATE TABLE sessions (
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
    content_type text,
    created_at timestamp,
    file text,
    CONSTRAINT fk_upload_session
        FOREIGN KEY (session_id)
        REFERENCES sessions(id),
    CONSTRAINT fk_file
        FOREIGN KEY (file)
        REFERENCES Files(path)
);