package db

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"os"
	"path/filepath"
	"runtime"
	"sync"

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

func PutUpload(session string, localFile string, size int, url string, filename string, contenttype string, uploaded_at string, id string) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.conn.Exec("INSERT INTO uploads (id, session_id, local_file, size, url, filename, contenttype, uploaded_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", id, session, localFile, size, url, filename, contenttype, uploaded_at)
	return err
}
