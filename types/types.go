package types

// Upload is the CDN metadata written to manifests and stored locally.
type Upload struct {
	URL           string `json:"url"`
	ID            string `json:"id"`
	Filename      string `json:"filename"`
	ContentType   string `json:"content_type"`
	Optimised     string `json:"optimised"`
	Size          int64  `json:"size"`
	CreatedAt     string `json:"created_at"`
	SourceSHA256  string `json:"source_sha256,omitempty"`
	PayloadSHA256 string `json:"payload_sha256,omitempty"`
	Reused        bool   `json:"reused"`
	Removed       bool   `json:"removed,omitempty"`
	FileLoc       string `json:"-"`
	VariantKey    string `json:"-"`
}

// File groups all requested variants of a source file.
type File struct {
	Uploads      []*Upload `json:"optimised"`
	Path         string    `json:"path"`
	RelativePath string    `json:"relative_path,omitempty"`
}

type Session struct {
	ID          string `json:"id"`
	CommandText string `json:"command_text"`
	FromDir     string `json:"from_dir"`
	CreatedAt   string `json:"created_at"`
	CompletedAt string `json:"completed_at,omitempty"`
	Status      string `json:"status"`
	Uploaded    int    `json:"uploaded"`
	Reused      int    `json:"reused"`
	Total       int    `json:"total"`
}

type SessionItem struct {
	SessionID  string
	UploadID   string
	SourcePath string
	Reused     bool
	RemovedAt  string
}
