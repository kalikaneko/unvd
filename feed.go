package nvd

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/timshannon/bolthold"
)

const (
	nvdDataFeeds     = "https://nvd.nist.gov/feeds/json/cve/1.1/nvdcve-1.1-%s.json.gz"
	nvdDataFeedsMeta = "https://nvd.nist.gov/feeds/json/cve/1.1/nvdcve-1.1-%s.meta"
	nvdCPEMatchFeed  = "https://nvd.nist.gov/feeds/json/cpematch/1.0/nvdcpematch-1.0.json.gz"
)

// ErrNotFound occurs when CVE is expected but no result is returned from fetch operations
var ErrNotFound = errors.New("CVE not found")

// Fetch description returns just the description for a given CVE ID.
func (c *Client) FetchDescription(cveID string) string {
	return ""
}

// FetchCVE extracts the year of a CVE ID, and returns a CVEItem data struct
// from the most up-to-date NVD data feed for that year
func (c *Client) FetchCVE(cveID string) (CVEItem, error) {
	if !IsCVEIDStrict(cveID) {
		return CVEItem{}, fmt.Errorf("invalid CVE ID: %s", cveID)
	}

	// TODO validate required data struct values before return

	cve, err := c.fetchNVDCVE(cveID)
	switch err {
	case nil:
		// Found cve in NVD feed, return result
		return cve, nil
	case ErrNotFound:
		// If not found in NVD feeds, fall back to check MITRE database
		// see if valid CVE ID exists with Reserved status
		cve, err = fetchReservedCVE(cveID)
		if err != nil {
			return CVEItem{}, ErrNotFound
		}
		return cve, nil
	default:
		// Case err != nil
		return CVEItem{}, err
	}
}

// FetchUpdatedCVEs returns a slice of most recently published and modified CVES
// from the previous eight days. This feed is updated approximately every two hours by NVD.
// NVD recommends that the "modified" feed should be used to keep up-to-date.
func (c *Client) FetchUpdatedCVEs() ([]CVEItem, error) {
	feedName := "modified"
	err := c.updateFeed(feedName)
	if err != nil {
		return nil, err
	}

	raw, err := c.loadFeed(feedName)
	if err != nil {
		return nil, err
	}

	var nvd NVDFeed
	err = json.Unmarshal(raw, &nvd)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling modified feed: %v", err)
	}
	return nvd.CVEItems, nil
}

func (c *Client) PrefetchYear(year string) error {
	p := c.pathToFeed(year)
	need := !isFileExists(p)
	if need {
		log.Println("Downloading year", year)
		err := c.downloadFeed(
			fmt.Sprintf(nvdDataFeeds, year),
			c.pathToFeed(year),
		)
		if err != nil {
			return fmt.Errorf("error fetching %s remote feed: %v", year, err)
		}
	} else {
		log.Println("Local data exists for", year)
	}
	err := c.createCompactSummary(year)
	if err != nil {
		return err
	}
	return nil
}

func (c *Client) openCompactDB(year string) (*bolthold.Store, error) {
	var store *bolthold.Store
	if store, ok := c.compactStores[year]; ok {
		return store, nil
	}
	store, err := bolthold.Open(c.pathToCompact(year), 0666, nil)
	if err != nil {
		return nil, err
	}
	c.compactStores[year] = store
	return store, nil
}

func (c *Client) GetDescription(cveID string) (string, error) {
	if !IsCVEIDStrict(cveID) {
		return "", fmt.Errorf("invalid CVE ID: %s", cveID)
	}
	year := strings.Split(cveID, "-")[1]
	if !isFileExists(c.pathToCompact(year)) {
		log.Println("Fetching feed for", year)
		err := c.createCompactSummary(year)
		if err != nil {
			return "", err
		}
	}
	shortID := strings.Split(cveID, "-")[2]

	store, err := c.openCompactDB(year)
	if err != nil {
		return "", err
	}
	res := []*CVEIndex{}
	store.Find(&res, bolthold.Where("ID").Eq(shortID))

	if len(res) == 0 {
		return "", fmt.Errorf("CVE not found: %s", cveID)
	} else if len(res) > 1 {
		return "", fmt.Errorf("Too many CVEs: %d", len(res))
	}
	return res[0].Description, nil
}

func (c *Client) createCompactSummary(year string) error {
	if isFileExists(c.pathToCompact(year)) {
		return nil
	}
	store, err := c.openCompactDB(year)
	if err != nil {
		return err
	}

	// the bottleneck is going to be writing to disk, but let's buffer a bit
	ch := make(chan CVEIndex, 500)

	// the compact summary only picks the fields we're interested in: description.
	// stage 1: iterate through all the json and emit only the useful fields
	f, err := os.Open(c.pathToFeed(year))
	if err != nil {
		return err
	}
	defer f.Close()

	decoder := json.NewDecoder(f)

	go func() {
		// Discard JSON tokens until reaching CVE_Items array
		for {

			tok, err := decoder.Token()
			if err != nil {
				break
			}
			if tok == "CVE_Items" {
				// Read next opening bracket
				decoder.Token()
				for decoder.More() {
					cve := cveInnerItem{}
					err = decoder.Decode(&cve)
					if err != nil {
						break
					}
					for _, d := range cve.CVE.Description.Data {
						idx := strings.Split(cve.CVE.Meta.ID, "-")[2]
						ch <- CVEIndex{ID: idx, Description: d.Value}
					}
				}
			}
		}
		close(ch)
	}()

	// stage 2: store those fields in a key-value store.
	log.Println("Inserting keys for", year)
	ctr := 0

	for cve := range ch {
		store.Insert(string(cve.ID), &cve)
		if ctr%500 == 0 {
			log.Printf("...inserted record #%d (CVE-%s-%s)\n", ctr, year, cve.ID)
		}
		ctr += 1
	}
	log.Println("inserted", ctr, "values")
	return nil
}

func (c *Client) updateFeed(year string) error {
	need, err := c.needNVDUpdate(year)
	if err != nil {
		return fmt.Errorf("error checking whether %s feed needs update: %v", year, err)
	}
	if need {
		err = c.downloadFeed(
			fmt.Sprintf(nvdDataFeeds, year),
			c.pathToFeed(year),
		)
		if err != nil {
			return fmt.Errorf("error fetching %s remote feed: %v", year, err)
		}
	}
	return nil
}

func (c *Client) fetchNVDCVE(cveID string) (cve CVEItem, err error) {
	yi, _ := ParseCVEID(cveID)
	year := strconv.Itoa(yi)

	err = c.updateFeed(year)
	if err != nil {
		return CVEItem{}, err
	}

	cve, err = c.searchFeed(year, cveID)
	if err != nil {
		if err == ErrNotFound {
			// pass ErrNotFound through to caller function
			return CVEItem{}, err
		}
		return CVEItem{}, fmt.Errorf("error fetching %s local feed: %v", year, err)
	}
	return cve, nil
}

func (c *Client) needNVDUpdate(year string) (bool, error) {
	// TODO remove redundant noNeed
	const need, noNeed bool = true, false

	var emptyMeta NVDMeta
	localMeta, err := c.fetchLocalMeta(year)
	if localMeta == emptyMeta && err == nil {
		return need, nil
	}

	remoteMeta, err := c.fetchRemoteMeta(year)
	if err != nil {
		return noNeed, err
	}

	if remoteMeta.Sha256 == localMeta.Sha256 {
		return noNeed, nil
	}
	return need, nil
}

func (c *Client) pathToFeed(year string) string {
	return path.Join(c.feedDir, fmt.Sprintf("%s.json", year))
}

func (c *Client) pathToMeta(year string) string {
	return path.Join(c.feedDir, fmt.Sprintf("%s.meta", year))
}

func (c *Client) pathToCompact(year string) string {
	return path.Join(c.feedDir, fmt.Sprintf("%s.db", year))
}

func (c *Client) loadFeed(year string) ([]byte, error) {
	p := c.pathToFeed(year)
	raw, err := ioutil.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("error reading local feed file %s: %v", p, err)
	}
	return raw, nil
}

func (c *Client) searchFeed(year string, cveID string) (CVEItem, error) {
	p := c.pathToFeed(year)
	f, err := os.Open(p)
	if err != nil {
		return CVEItem{}, err
	}
	defer f.Close()

	decoder := json.NewDecoder(f)

	// Discard JSON tokens until reaching CVE_Items array
	for {
		tok, err := decoder.Token()
		if err != nil {
			return CVEItem{}, err
		}
		if tok == "CVE_Items" {
			// Read next opening bracket
			decoder.Token()
			break
		}
	}

	var cve CVEItem
	for decoder.More() {
		err = decoder.Decode(&cve)
		if err != nil {
			return CVEItem{}, err
		}

		if cve.CVE.CVEDataMeta.ID == cveID {
			return cve, nil
		}
	}

	return CVEItem{}, ErrNotFound
}

// downloadFeed downloads a gz compressed feed file from u url to p file path
func (c *Client) downloadFeed(u, p string) (err error) {
	// open the uri
	resp, err := http.Get(u)
	if err != nil {
		return fmt.Errorf("error http request to %s: %v", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	// create a gzip reader over body reader
	archive, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer archive.Close()

	// create the file sink

	file, err := os.Create(p)
	if err != nil {
		return fmt.Errorf("error creating local feed file %s: %v", p, err)
	}
	defer file.Close()

	_, err = io.Copy(file, archive)
	return err
}

func (c *Client) fetchLocalMeta(year string) (NVDMeta, error) {
	var meta NVDMeta
	p := c.pathToMeta(year)
	raw, err := ioutil.ReadFile(p)
	if err != nil {
		return meta, err
	}

	return parseRawNVDMeta(raw), nil
}

// FetchNvdMeta fetch NVD meta data
func (c *Client) fetchRemoteMeta(year string) (NVDMeta, error) {
	var meta NVDMeta

	url := fmt.Sprintf(nvdDataFeedsMeta, year)
	resp, err := http.Get(url)
	if err != nil {
		return meta, err
	}
	defer resp.Body.Close()

	byteArray, _ := ioutil.ReadAll(resp.Body)
	err = c.saveNVDMeta(year, byteArray)
	if err != nil {
		return meta, err
	}

	meta = parseRawNVDMeta(byteArray)
	return meta, nil
}

// parseRawNVDMeta convert meta data to NVDMeta
func parseRawNVDMeta(meta []byte) NVDMeta {
	var metaModel NVDMeta

	result := regexp.MustCompile("\r\n|\n\r|\n|\r").Split(string(meta), -1)
	metaModel.LastModifiedDate = regexp.MustCompile(":").Split(result[0], -1)[1]
	metaModel.Size = regexp.MustCompile(":").Split(result[1], -1)[1]
	metaModel.ZipSize = regexp.MustCompile(":").Split(result[2], -1)[1]
	metaModel.GzSize = regexp.MustCompile(":").Split(result[3], -1)[1]
	metaModel.Sha256 = regexp.MustCompile(":").Split(result[4], -1)[1]

	return metaModel
}

// saveNVDMeta store meta data to feeds/
func (c *Client) saveNVDMeta(year string, meta []byte) error {
	p := c.pathToMeta(year)
	file, err := os.Create(p)
	if err != nil {
		return fmt.Errorf("error creating %s: %v", p, err)
	}
	defer file.Close()

	_, err = file.Write(meta)
	if err != nil {
		return fmt.Errorf("error writing to %s: %v", p, err)
	}
	return nil
}

func isFileExists(filePath string) bool {
	if _, err := os.Stat(filePath); err == nil {
		return true
	}
	return false
}
