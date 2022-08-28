package nvd

import (
	"os"
	"path"

	"github.com/timshannon/bolthold"
)

type Client struct {
	feedDir       string
	compactStores map[string]*bolthold.Store
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
