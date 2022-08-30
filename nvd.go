package nvd

import (
	"database/sql"
	"os"
	"path"

	_ "github.com/mattn/go-sqlite3"
	"github.com/timshannon/bolthold"
)

type Client struct {
	feedDir       string
	compactStores map[string]*bolthold.Store
	Sqlite        bool
	db            *sql.DB
}

func NewClient(baseDir string) (cl *Client, err error) {
	if baseDir == "" {
		baseDir = os.Getenv("PWD")
	}
	feedDir := path.Join(baseDir, "feeds")

	// Check if feeds dir exists, if not create it
	if _, err := os.Stat(feedDir); os.IsNotExist(err) {
		if err := os.Mkdir(feedDir, 0700); err != nil {
			return nil, err
		}
	}

	compactStores := make(map[string]*bolthold.Store)

	return &Client{
		feedDir:       feedDir,
		compactStores: compactStores,
	}, nil
}
