package db

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/louis-bourgault/hccdn-cli/types"
	_ "github.com/ncruces/go-sqlite3/driver"
)

//go:embed init.sql
var InitDBScript string

type Db struct {
	conn *sql.DB
}

var (
	instance *Db
	once     sync.Once
)

func GetDB() (*Db, error) {
	var err error
	once.Do(func() {
		var baseDir string

		if runtime.GOOS == "darwin" {
			baseDir, err = os.UserConfigDir()
			if err != nil {
				return
			}
		} else if runtime.GOOS == "windows" {
			baseDir, err = os.UserConfigDir()
			if err != nil {
				return
			}
		} else if XDG_CONFIG_HOME := os.Getenv("XDG_CONFIG_HOME"); XDG_CONFIG_HOME != "" {
			baseDir = XDG_CONFIG_HOME
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return
			}
			baseDir = filepath.Join(home, ".local", "share")
		}

		pathToDB := baseDir + "/hccdn-cli/hccdn.db"
		fmt.Printf("db path: %s\n", pathToDB)
		stat, err := os.Stat(pathToDB)
		os.MkdirAll(baseDir+"/hccdn-cli", os.ModePerm)
		if os.IsNotExist(err) || stat.IsDir() {
			setupDB(pathToDB)
		}
		var conn *sql.DB
		conn, err = sql.Open("sqlite3", pathToDB)

		if err != nil {
			return
		}
		instance = &Db{conn: conn}
		instance.conn.Exec("PRAGMA foreign_keys = ON;")
	})
	return instance, err
}

func setupDB(loc string) error {
	fmt.Printf("setting up db at %s\n", loc)
	//first time setup
	conn, err := sql.Open("sqlite3", loc)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Exec(InitDBScript)
	return err
}

func BeginSession(commandtext, fromdir string) (id string, err error) {
	db, err := GetDB()
	if err != nil {
		return "", err
	}
	newID := genID()
	_, err = db.conn.Exec("INSERT INTO sessions (id, command_text, from_dir) VALUES (?, ?, ?)", newID, commandtext, fromdir)
	if err != nil {
		return "", err
	}
	return newID, nil
}

func genID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	bytes := make([]byte, 8)

	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}

	for i, b := range bytes {
		bytes[i] = charset[b%byte(len(charset))]
	}

	return string(bytes)
}

func PutFile(path string) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.conn.Exec("INSERT INTO files (path) VALUES (?)", path)
	return err
}

func GetChildFiles(path string) ([]string, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.conn.Query("SELECT path FROM files WHERE path LIKE ?", path+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		err := rows.Scan(&p)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

func SaveUpload(upload *types.Upload, sessionID string) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.conn.Exec(
		"INSERT INTO Uploads (id, filename, size, session_id, url, content_type, created_at, file) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		upload.Id,
		upload.Filename,
		upload.Size,
		sessionID,
		upload.Url,
		upload.ContentType,
		upload.CreatedAt,
		upload.FileLoc,
	)
	return err
}

func DeleteUpload(id string) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.conn.Exec("DELETE FROM uploads WHERE id = ?", id)
	return err
}

func GetUploadsBySession(sessionID string) ([]types.Upload, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.conn.Query("SELECT filename, size, url, content_type, created_at, id FROM uploads WHERE session_id = ?", sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uploads []types.Upload
	for rows.Next() {
		var upload types.Upload
		err := rows.Scan(&upload.Filename, &upload.Size, &upload.Url, &upload.ContentType, &upload.CreatedAt, &upload.Id)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
		fmt.Printf("found upload in session: %+v\n", upload)
	}
	return uploads, nil
}

func DeleteSession(id string) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.conn.Exec("DELETE FROM sessions WHERE id = ?", id)
	return err
}

func GetSessionById(id string) (session types.Session, err error, exists bool) {
	db, err := GetDB()
	if err != nil {
		return types.Session{}, err, false
	}
	fmt.Printf("getting session by id: %s\n", id)
	row := db.conn.QueryRow("SELECT id, command_text, from_dir FROM sessions WHERE id = ?", id)
	var s types.Session
	err = row.Scan(&s.Id, &s.CommandText, &s.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			fmt.Printf("session with id %s does not exist\n", id)
			return types.Session{}, nil, false

		}
		fmt.Printf("error scanning session: %s\n", err)
		return types.Session{}, err, false
	}
	fmt.Printf("found session: %+v\n", s)
	return s, nil, true
}

func GetUploadsByFilename(originalFilepath string) ([]types.Upload, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.conn.Query("SELECT filename, size, url, content_type, created_at, id FROM uploads WHERE file = ?", originalFilepath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uploads []types.Upload
	for rows.Next() {
		var upload types.Upload
		err = rows.Scan(&upload.Filename, &upload.Size, &upload.Url, &upload.ContentType, &upload.CreatedAt, &upload.Id)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, nil
			}
			return nil, err
		}
		uploads = append(uploads, upload)

	}
	return uploads, nil
}

func GetAllUploads() ([]types.Upload, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.conn.Query("SELECT filename, size, url, content_type, created_at, id FROM uploads")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uploads []types.Upload
	for rows.Next() {
		var upload types.Upload
		err := rows.Scan(&upload.Filename, &upload.Size, &upload.Url, &upload.ContentType, &upload.CreatedAt, &upload.Id)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	return uploads, nil
}
