package main

import (
	"bytes"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyTitleMigration(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "gallery.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE photos (id TEXT PRIMARY KEY, filename TEXT NOT NULL UNIQUE, label TEXT NOT NULL DEFAULT '', hidden INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL);
	INSERT INTO photos(id, filename, label, hidden, created_at) VALUES('old', 'old.jpg', 'Original caption', 1, '2020-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	cfg := config{dataDir: dir, familyPassword: "family-password", adminUser: "admin", adminPassword: "admin-password", sessionKey: strings.Repeat("k", 32)}
	for i := 0; i < 2; i++ {
		app, err := newApplication(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var title string
		if err := app.db.QueryRow(`SELECT title FROM photos WHERE id = 'old'`).Scan(&title); err != nil || title != "Original caption" {
			t.Fatalf("migration %d: %q, %v", i, title, err)
		}
		app.db.Close()
	}
}

func requestWithSession(t *testing.T, app *application, method, path, scope, csrf string, fields url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if fields == nil {
		fields = url.Values{}
	}
	if csrf != "" {
		fields.Set("csrf", csrf)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(fields.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if scope != "" {
		w := httptest.NewRecorder()
		app.setSession(w, scope)
		for _, c := range w.Result().Cookies() {
			r.AddCookie(c)
		}
		if csrf == "valid" {
			fields.Set("csrf", app.sessionCSRF(r, scope))
			r = httptest.NewRequest(method, path, strings.NewReader(fields.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for _, c := range w.Result().Cookies() {
				r.AddCookie(c)
			}
		}
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	return w
}

func TestTitleUpdateAndUploadStorage(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "first", false)
	w := requestWithSession(t, app, "POST", "/admin/photos/first/title", "admin", "valid", url.Values{"title": {"  New title  "}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("title update: %d: %s", w.Code, w.Body.String())
	}
	var title string
	if err := app.db.QueryRow(`SELECT title FROM photos WHERE id = 'first'`).Scan(&title); err != nil || title != "New title" {
		t.Fatalf("stored title: %q %v", title, err)
	}
	var body bytes.Buffer
	session := httptest.NewRecorder()
	app.setSession(session, "admin")
	r := httptest.NewRequest("POST", "/admin/photos", nil)
	for _, c := range session.Result().Cookies() {
		r.AddCookie(c)
	}
	// Single upload parses the multipart CSRF from the body.
	form := multipart.NewWriter(&body)
	form.WriteField("csrf", app.sessionCSRF(r, "admin"))
	form.WriteField("title", "Uploaded title")
	part, _ := form.CreateFormFile("photo", "photo.png")
	part.Write(tinyPNG)
	form.Close()
	r = httptest.NewRequest("POST", "/admin/photos", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	for _, c := range session.Result().Cookies() {
		r.AddCookie(c)
	}
	w = httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	if err := app.db.QueryRow(`SELECT title FROM photos WHERE id != 'first'`).Scan(&title); err != nil || title != "Uploaded title" {
		t.Fatalf("upload title: %q %v", title, err)
	}
	if err := app.storeBatch([]uploadImage{{data: tinyPNG, ext: ".png"}}, "Batch title"); err != nil {
		t.Fatal(err)
	}
	if err := app.db.QueryRow(`SELECT title FROM photos WHERE title = 'Batch title'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
}

func TestBulkUploadAcceptsTitleField(t *testing.T) {
	app := testApplication(t)
	session := httptest.NewRecorder()
	app.setSession(session, "admin")
	r := httptest.NewRequest("POST", "/admin/bulk-upload", nil)
	for _, c := range session.Result().Cookies() {
		r.AddCookie(c)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	form.WriteField("csrf", app.sessionCSRF(r, "admin"))
	form.WriteField("title", "Shared trip")
	part, _ := form.CreateFormFile("photos", "trip.png")
	part.Write(tinyPNG)
	form.Close()
	r = httptest.NewRequest("POST", "/admin/bulk-upload", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	for _, c := range session.Result().Cookies() {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("bulk upload: %d %s", w.Code, w.Body.String())
	}
	var title string
	if err := app.db.QueryRow(`SELECT title FROM photos`).Scan(&title); err != nil || title != "Shared trip" {
		t.Fatalf("bulk title: %q %v", title, err)
	}
}

func TestTagsReuseClearAndVisibility(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "first", false)
	addTestPhoto(t, app, "second", false)
	addTestPhoto(t, app, "hidden", true)
	if _, err := app.db.Exec(`UPDATE photos SET title = 'Hidden title private' WHERE id = 'hidden'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, tags string }{{"first", " Holiday, holiday, Family, ÉTÉ "}, {"second", "HOLIDAY, été"}, {"hidden", "SecretOnly"}} {
		w := requestWithSession(t, app, "POST", "/admin/photos/"+tc.id+"/tags", "admin", "valid", url.Values{"tags": {tc.tags}})
		if w.Code != http.StatusSeeOther {
			t.Fatalf("save %s: %d %s", tc.id, w.Code, w.Body.String())
		}
	}
	var count int
	app.db.QueryRow(`SELECT count(*) FROM tags`).Scan(&count)
	if count != 4 {
		t.Fatalf("unique tags: %d", count)
	}
	app.db.QueryRow(`SELECT count(*) FROM photo_tags`).Scan(&count)
	if count != 6 {
		t.Fatalf("tag links: %d", count)
	}
	w := requestWithSession(t, app, "GET", "/", "family", "", nil)
	html := w.Body.String()
	if strings.Contains(html, "SecretOnly") || strings.Contains(html, "Hidden title private") || strings.Contains(html, "/media/hidden") {
		t.Fatal("hidden photo metadata leaked")
	}
	for _, part := range []string{`data-filter="holiday"`, `data-filter="family"`, `aria-pressed="true"`, `id="filter-empty"`, `data-tags=`, `src="/static/gallery.js"`, `Selections remain checked across filters.`} {
		if !strings.Contains(html, part) {
			t.Fatalf("missing gallery filtering markup: %s", part)
		}
	}
	w = requestWithSession(t, app, "GET", "/admin", "admin", "", nil)
	if !strings.Contains(w.Body.String(), "SecretOnly") || !strings.Contains(w.Body.String(), `action="/admin/photos/hidden/tags"`) {
		t.Fatal("hidden tags missing in admin")
	}
	w = requestWithSession(t, app, "POST", "/admin/photos/first/tags", "admin", "valid", url.Values{"tags": {"  "}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("clear: %d", w.Code)
	}
	app.db.QueryRow(`SELECT count(*) FROM tags`).Scan(&count)
	if count != 3 {
		t.Fatalf("orphan tags remain after clear: %d", count)
	}
	for _, id := range []string{"second", "hidden"} {
		w = requestWithSession(t, app, "POST", "/admin/photos/"+id+"/delete", "admin", "valid", nil)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("delete: %d %s", w.Code, w.Body.String())
		}
	}
	app.db.QueryRow(`SELECT count(*) FROM tags`).Scan(&count)
	if count != 0 {
		t.Fatalf("orphan tags after deletion: %d", count)
	}
}

func TestTagUnicodeFoldAndReuse(t *testing.T) {
	for _, names := range [][]string{
		{"ΟΣ", "ος", "οσ"},
		{"Straße", "STRASSE", "STRAẞE"},
		{"ÉTÉ", "E\u0301TE\u0301", "été"},
	} {
		key := tagKey(names[0])
		for _, name := range names[1:] {
			if tagKey(name) != key {
				t.Fatalf("%q and %q must share a key: %q vs %q", names[0], name, key, tagKey(name))
			}
		}
		tags, err := parseTags(strings.Join(names, ","))
		if err != nil || len(tags) != 1 || tags[0] != names[0] {
			t.Fatalf("dedup %v: %v, %v", names, tags, err)
		}
	}
	app := testApplication(t)
	addTestPhoto(t, app, "first", false)
	addTestPhoto(t, app, "second", false)
	for id, value := range map[string]string{"first": "ΟΣ, ος, Straße, ÉTÉ", "second": "οσ, STRASSE, E\u0301TE\u0301"} {
		w := requestWithSession(t, app, "POST", "/admin/photos/"+id+"/tags", "admin", "valid", url.Values{"tags": {value}})
		if w.Code != http.StatusSeeOther {
			t.Fatalf("save %s: %d: %s", id, w.Code, w.Body.String())
		}
	}
	var count int
	if err := app.db.QueryRow(`SELECT count(*) FROM tags`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("tags: %d %v", count, err)
	}
	if err := app.db.QueryRow(`SELECT count(*) FROM photo_tags`).Scan(&count); err != nil || count != 6 {
		t.Fatalf("links: %d %v", count, err)
	}
	w := requestWithSession(t, app, "GET", "/", "family", "", nil)
	for _, part := range []string{`data-filter="οσ"`, `data-filter="strasse"`, `data-filter="été"`, `data-tags="[`, `οσ`, `strasse`} {
		if !strings.Contains(w.Body.String(), part) {
			t.Fatalf("gallery missing canonical filter/card key %q", part)
		}
	}
}

func TestMigrationMergesOlderUnicodeTagKeys(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "first", false)
	addTestPhoto(t, app, "second", false)
	// Simulate legacy strings.ToLower keys, including both links on one photo.
	_, err := app.db.Exec(`INSERT INTO tags(id, name, key) VALUES (1, 'ΟΣ', 'ος'), (2, 'οσ', 'οσ');
		INSERT INTO photo_tags(photo_id, tag_id) VALUES ('first', 1), ('first', 2), ('second', 2)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrateDatabase(app.db); err != nil {
			t.Fatal(err)
		}
	}
	var key, name string
	if err := app.db.QueryRow(`SELECT key, name FROM tags`).Scan(&key, &name); err != nil || key != tagKey("ΟΣ") || name != "ΟΣ" {
		t.Fatalf("merged tag: %q %q %v", key, name, err)
	}
	var count int
	if err := app.db.QueryRow(`SELECT count(*) FROM photo_tags`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("merged links: %d %v", count, err)
	}
	if err := app.saveTags("second", []string{"ος"}); err != nil {
		t.Fatal(err)
	}
	if err := app.db.QueryRow(`SELECT count(*) FROM tags`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("reuse after migration: %d %v", count, err)
	}
}

func TestTagValidationAndAuthorization(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "first", false)
	for _, scope := range []string{"", "family"} {
		w := requestWithSession(t, app, "POST", "/admin/photos/first/tags", scope, "valid", url.Values{"tags": {"Forbidden"}})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%q session: %d", scope, w.Code)
		}
	}
	w := requestWithSession(t, app, "POST", "/admin/photos/first/tags", "admin", "bad", url.Values{"tags": {"Forbidden"}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad CSRF: %d", w.Code)
	}
	for _, input := range []string{"valid,", strings.Repeat("a", 41), "newline\ninvalid", "a,b,c,d,e,f,g,h,i,j,k"} {
		w = requestWithSession(t, app, "POST", "/admin/photos/first/tags", "admin", "valid", url.Values{"tags": {input}})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid %q: %d", input, w.Code)
		}
	}
	var count int
	app.db.QueryRow(`SELECT count(*) FROM tags`).Scan(&count)
	if count != 0 {
		t.Fatalf("unauthorized or invalid tags saved: %d", count)
	}
}

func TestBulkDeleteCascadesTags(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "first", false)
	addTestPhoto(t, app, "second", true)
	addTestPhoto(t, app, "keeper", false)
	for _, id := range []string{"first", "second", "keeper"} {
		if err := app.saveTags(id, []string{"Shared"}); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, deleteRequest(t, app, "admin", []string{"first", "second"}, true))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("bulk delete: %d %s", w.Code, w.Body.String())
	}
	var count int
	if err := app.db.QueryRow(`SELECT count(*) FROM photo_tags`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("remaining links: %d %v", count, err)
	}
	if err := app.db.QueryRow(`SELECT count(*) FROM tags`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("shared tag removed: %d %v", count, err)
	}
}
