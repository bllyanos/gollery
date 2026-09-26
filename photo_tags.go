package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// tagKey is shared by tag storage, deduplication, and gallery filter values.
func tagKey(name string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(name)))
}

// Migrate in one transaction so an interrupted upgrade cannot leave partial schema.
func migrateDatabase(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS photos (
		id TEXT PRIMARY KEY, filename TEXT NOT NULL UNIQUE, title TEXT NOT NULL DEFAULT '',
		hidden INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	rows, err := tx.Query(`PRAGMA table_info(photos)`)
	if err != nil {
		return err
	}
	var hasTitle, hasLabel bool
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		hasTitle = hasTitle || name == "title"
		hasLabel = hasLabel || name == "label"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if hasLabel && !hasTitle {
		if _, err = tx.Exec(`ALTER TABLE photos RENAME COLUMN label TO title`); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS tags (
		id INTEGER PRIMARY KEY, name TEXT NOT NULL, key TEXT NOT NULL UNIQUE
	); CREATE TABLE IF NOT EXISTS photo_tags (
		photo_id TEXT NOT NULL REFERENCES photos(id) ON DELETE CASCADE,
		tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
		PRIMARY KEY (photo_id, tag_id)
	); CREATE INDEX IF NOT EXISTS photo_tags_tag_id ON photo_tags(tag_id)`); err != nil {
		return err
	}
	if err = migrateTagKeys(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Reconcile keys written by older versions (which used strings.ToLower).
// Preserve the oldest spelling and transfer every photo link before removing duplicates.
func migrateTagKeys(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id, name FROM tags ORDER BY id`)
	if err != nil {
		return err
	}
	type storedTag struct {
		id  int64
		key string
	}
	tags := []storedTag{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return err
		}
		tags = append(tags, storedTag{id, tagKey(name)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	winners := make(map[string]int64, len(tags))
	for _, tag := range tags {
		if winner, ok := winners[tag.key]; ok {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO photo_tags(photo_id, tag_id) SELECT photo_id, ? FROM photo_tags WHERE tag_id = ?`, winner, tag.id); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM photo_tags WHERE tag_id = ?`, tag.id); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM tags WHERE id = ?`, tag.id); err != nil {
				return err
			}
			continue
		}
		winners[tag.key] = tag.id
	}
	for key, id := range winners {
		if _, err := tx.Exec(`UPDATE tags SET key = ? WHERE id = ?`, key, id); err != nil {
			return err
		}
	}
	return nil
}

func (a *application) loadPhotos(includeHidden bool) ([]photo, error) {
	query := `SELECT id, title, created_at, hidden FROM photos`
	if !includeHidden {
		query += ` WHERE hidden = 0`
	}
	query += ` ORDER BY created_at DESC`
	rows, err := a.db.Query(query)
	if err != nil {
		return nil, err
	}
	photos := []photo{}
	for rows.Next() {
		var p photo
		if err := rows.Scan(&p.ID, &p.Title, &p.CreatedAt, &p.Hidden); err != nil {
			rows.Close()
			return nil, err
		}
		photos = append(photos, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Always constrain family tag reads in SQL, not only while rendering.
	query = `SELECT pt.photo_id, t.name FROM photo_tags pt JOIN tags t ON t.id = pt.tag_id JOIN photos p ON p.id = pt.photo_id`
	if !includeHidden {
		query += ` WHERE p.hidden = 0`
	}
	query += ` ORDER BY t.name COLLATE NOCASE`
	rows, err = a.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[string]*photo, len(photos))
	for i := range photos {
		byID[photos[i].ID] = &photos[i]
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if p := byID[id]; p != nil {
			p.Tags = append(p.Tags, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range photos {
		photos[i].TagsText = strings.Join(photos[i].Tags, ", ")
	}
	return photos, nil
}

func parseTags(input string) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return []string{}, nil
	}
	tags := []string{}
	seen := map[string]bool{}
	for _, raw := range strings.Split(input, ",") {
		name := strings.TrimSpace(raw)
		if name == "" || utf8.RuneCountInString(name) > 40 || strings.ContainsFunc(name, unicode.IsControl) {
			return nil, errors.New("tags must be nonempty, at most 40 characters, and contain no control characters")
		}
		key := tagKey(name)
		if !seen[key] {
			seen[key] = true
			tags = append(tags, name)
			if len(tags) > 10 {
				return nil, errors.New("a photo may have at most 10 tags")
			}
		}
	}
	return tags, nil
}

func (a *application) updateTags(w http.ResponseWriter, r *http.Request) {
	if !a.requireSession(w, r, "admin") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if !a.checkSessionCSRF(r, "admin") {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if !safePhotoID(r.PathValue("id")) {
		http.NotFound(w, r)
		return
	}
	tags, err := parseTags(r.PostForm.Get("tags"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.saveTags(r.PathValue("id"), tags); err != nil {
		if errors.Is(err, errPhotoMissing) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "Unable to update tags", http.StatusInternalServerError)
		}
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *application) saveTags(id string, tags []string) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM photos WHERE id = ?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return errPhotoMissing
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM photo_tags WHERE photo_id = ?`, id); err != nil {
		return err
	}
	for _, name := range tags {
		key := tagKey(name)
		if _, err := tx.Exec(`INSERT INTO tags(name, key) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`, name, key); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO photo_tags(photo_id, tag_id) SELECT ?, id FROM tags WHERE key = ?`, id, key); err != nil {
			return fmt.Errorf("link tag: %w", err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM tags WHERE NOT EXISTS (SELECT 1 FROM photo_tags WHERE tag_id = tags.id)`); err != nil {
		return err
	}
	return tx.Commit()
}
