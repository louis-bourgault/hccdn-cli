package types

type Upload struct {
	Url         string `json:"url"`
	Id          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Optimised   string `json:"optimised"` //can be empty if not optimised, otherwise a text like "full" (for optimised og size but webp) or "720" (for maxwidth/height 720px)
	Size        int    `json:"size"`
	CreatedAt   string `json:"created_at"`
	FileLoc     string
}

type File struct {
	Uploads []*Upload `json:"optimised"`
	Path    string    `json:"path"`
}

type Session struct {
	Id          string
	CommandText string
	FromDir     string //the direc wher the command was run from
	CreatedAt   string //is timestamp in db
}
