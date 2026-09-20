// Package photoslib reads a macOS Photos library.
//
// It opens a copy of the library's SQLite database, never the original: Photos
// holds a write lock while it runs, and a reader that waits on it would hang
// for as long as the app is open.
package photoslib

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// coreDataEpoch is 2001-01-01 UTC: Photos stores dates as seconds from it.
const coreDataEpoch = 978307200

// DefaultPath is where Photos keeps the system library.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Pictures", "Photos Library.photoslibrary")
}

// Item is one visible photo or video in the library. Hidden items, items in
// Recently Deleted, and the frames of a burst that were not picked are not
// items: Photos does not count them either.
type Item struct {
	UUID      string    `json:"uuid"`
	Filename  string    `json:"filename"` // the name the camera gave it
	Taken     time.Time `json:"taken"`    // date taken, in the item's own time zone
	IsVideo   bool      `json:"is_video"`
	IsLive    bool      `json:"is_live"`
	IsEdited  bool      `json:"is_edited"`
	Original  string    `json:"original"` // absolute path of the unedited file
	SizeBytes int64     `json:"size_bytes"`
}

// Filter narrows a listing. A zero Filter lists every visible item.
type Filter struct {
	UUIDs     []string
	From, To  time.Time
	VideoOnly bool
	PhotoOnly bool
	LiveOnly  bool
	EditsOnly bool
	Limit     int
}

// Library is an open copy of a Photos library database.
type Library struct {
	path string
	db   *sql.DB
	tmp  string
}

// Open copies the database out of the library and opens it read-only. Close
// removes the copy.
func Open(libraryPath string) (*Library, error) {
	if libraryPath == "" {
		libraryPath = DefaultPath()
	}
	if _, err := os.Stat(filepath.Join(libraryPath, "database", "Photos.sqlite")); err != nil {
		return nil, fmt.Errorf("%s is not a Photos library: %w", libraryPath, err)
	}
	tmp, err := os.MkdirTemp("", "ptg-photoslib-")
	if err != nil {
		return nil, err
	}
	// The -wal file holds writes Photos has not checkpointed yet; copying it
	// with the database is what makes the copy current rather than stale.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := filepath.Join(libraryPath, "database", "Photos.sqlite"+suffix)
		data, err := os.ReadFile(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			os.RemoveAll(tmp)
			return nil, fmt.Errorf("cannot read the library database (give your terminal Full Disk Access): %w", err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "Photos.sqlite"+suffix), data, 0o600); err != nil {
			os.RemoveAll(tmp)
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(tmp, "Photos.sqlite")+"?mode=ro")
	if err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	return &Library{path: libraryPath, db: db, tmp: tmp}, nil
}

func (l *Library) Close() error {
	err := l.db.Close()
	os.RemoveAll(l.tmp)
	return err
}

// Path is the library this was opened from.
func (l *Library) Path() string { return l.path }

// Items lists the visible items that match f, oldest first.
func (l *Library) Items(f Filter) ([]Item, error) {
	query := `
SELECT z.ZUUID, IFNULL(a.ZORIGINALFILENAME, z.ZFILENAME),
       z.ZDATECREATED + ? + IFNULL(a.ZTIMEZONEOFFSET, 0),
       z.ZKIND, IFNULL(z.ZKINDSUBTYPE, 0), IFNULL(z.ZADJUSTMENTSSTATE, 0),
       IFNULL(z.ZDIRECTORY, ''), IFNULL(z.ZFILENAME, ''), IFNULL(a.ZORIGINALFILESIZE, 0)
FROM ZASSET z
LEFT JOIN ZADDITIONALASSETATTRIBUTES a ON a.ZASSET = z.Z_PK
WHERE z.ZTRASHEDSTATE = 0 AND z.ZHIDDEN = 0 AND z.ZVISIBILITYSTATE = 0`
	args := []any{float64(coreDataEpoch)}

	if len(f.UUIDs) > 0 {
		query += " AND z.ZUUID IN (" + strings.TrimSuffix(strings.Repeat("?,", len(f.UUIDs)), ",") + ")"
		for _, u := range f.UUIDs {
			args = append(args, strings.ToUpper(u))
		}
	}
	if !f.From.IsZero() {
		query += " AND z.ZDATECREATED >= ?"
		args = append(args, float64(f.From.Unix()-coreDataEpoch))
	}
	if !f.To.IsZero() {
		query += " AND z.ZDATECREATED < ?"
		args = append(args, float64(f.To.Unix()-coreDataEpoch))
	}
	switch {
	case f.VideoOnly:
		query += " AND z.ZKIND = 1"
	case f.PhotoOnly:
		query += " AND z.ZKIND = 0"
	}
	if f.LiveOnly {
		query += " AND z.ZKINDSUBTYPE = 2"
	}
	if f.EditsOnly {
		query += " AND z.ZADJUSTMENTSSTATE > 0"
	}
	query += " ORDER BY z.ZDATECREATED"
	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
	}

	rows, err := l.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cannot read the library (its schema may be newer than ptg knows): %w", err)
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var (
			it                         Item
			taken                      float64
			kind, subtype, adjustments int
			dir, file                  string
		)
		if err := rows.Scan(&it.UUID, &it.Filename, &taken, &kind, &subtype, &adjustments, &dir, &file, &it.SizeBytes); err != nil {
			return nil, err
		}
		it.Taken = time.Unix(int64(taken), 0).UTC()
		it.IsVideo = kind == 1
		it.IsLive = subtype == 2
		it.IsEdited = adjustments > 0
		if dir != "" && file != "" {
			it.Original = filepath.Join(l.path, "originals", dir, file)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// ExportedFiles lists the files osxphotos wrote into dir, read from the export
// database it keeps there. Files that were removed since are left out.
func ExportedFiles(dir string) ([]string, error) {
	path := filepath.Join(dir, ".osxphotos_export.db")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no export database in %s: osxphotos writes one as it exports", dir)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query("SELECT filepath FROM export_data ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []string
	for rows.Next() {
		var rel string
		if err := rows.Scan(&rel); err != nil {
			return nil, err
		}
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	return files, rows.Err()
}
