package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deleteRequest(t *testing.T, app *application, scope string, ids []string, token bool) *http.Request {
	t.Helper()
	values := url.Values{}
	for _, id := range ids {
		values.Add("photo_id", id)
	}
	r := httptest.NewRequest("POST", "/admin/photos/delete", strings.NewReader(values.Encode()))
	if scope != "" {
		w := httptest.NewRecorder()
		app.setSession(w, scope)
		for _, cookie := range w.Result().Cookies() {
			r.AddCookie(cookie)
		}
		if token {
			values.Set("csrf", app.sessionCSRF(r, scope))
			r = httptest.NewRequest("POST", "/admin/photos/delete", strings.NewReader(values.Encode()))
			for _, cookie := range w.Result().Cookies() {
				r.AddCookie(cookie)
			}
		}
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestBulkDeleteValidation(t *testing.T) {
	for _, tc := range []struct {
		name, scope string
		ids         []string
		token       bool
		status      int
	}{
		{"anonymous", "", []string{"visible"}, false, 401},
		{"family", "family", []string{"visible"}, true, 401},
		{"missing csrf", "admin", []string{"visible"}, false, 400},
		{"empty", "admin", nil, true, 400},
		{"duplicate", "admin", []string{"visible", "visible"}, true, 400},
		{"invalid", "admin", []string{"visible", "../escape"}, true, 400},
		{"missing", "admin", []string{"visible", "absent"}, true, 404},
		{"too many", "admin", makeIDs(51), true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApplication(t)
			addTestPhoto(t, app, "visible", false)
			w := httptest.NewRecorder()
			app.routes().ServeHTTP(w, deleteRequest(t, app, tc.scope, tc.ids, tc.token))
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			assertPhotoCount(t, app, 1)
		})
	}
}

func makeIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "visible"
	}
	return ids
}

func TestBulkDeleteRemovesVisibleAndHiddenFiles(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "visible", false)
	addTestPhoto(t, app, "hidden", true)
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, deleteRequest(t, app, "admin", []string{"visible", "hidden"}, true))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin" {
		t.Fatalf("delete: %d: %s", w.Code, w.Body.String())
	}
	assertPhotoCount(t, app, 0)
}

func TestBulkDeleteDatabaseFailureRestoresFiles(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "visible", false)
	addTestPhoto(t, app, "hidden", true)
	if _, err := app.db.Exec(`CREATE TRIGGER fail_delete BEFORE DELETE ON photos WHEN OLD.id = 'hidden' BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, deleteRequest(t, app, "admin", []string{"visible", "hidden"}, true))
	if w.Code != 500 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	assertPhotoCount(t, app, 2)
}

func TestBulkDeleteRejectsUnsafeStoredFilename(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "visible", false)
	outside := filepath.Join(app.cfg.dataDir, "outside.jpg")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec(`UPDATE photos SET filename = '../outside.jpg' WHERE id = 'visible'`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, deleteRequest(t, app, "admin", []string{"visible"}, true))
	if w.Code != 500 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "keep" {
		t.Fatalf("outside file changed: %v", err)
	}
	assertPhotoCount(t, app, 1)
}

func TestDeletePhotoStillWorksForHiddenPhoto(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "hidden", true)
	session := httptest.NewRecorder()
	app.setSession(session, "admin")
	r := httptest.NewRequest("POST", "/admin/photos/hidden/delete", nil)
	for _, cookie := range session.Result().Cookies() {
		r.AddCookie(cookie)
	}
	r = httptest.NewRequest("POST", "/admin/photos/hidden/delete", strings.NewReader(url.Values{"csrf": {app.sessionCSRF(r, "admin")}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range session.Result().Cookies() {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("individual delete: %d: %s", w.Code, w.Body.String())
	}
	assertPhotoCount(t, app, 0)
}

func TestBulkDeleteCleansStaleMetadata(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "missing-file", false)
	if err := os.Remove(filepath.Join(app.cfg.dataDir, "photos", "missing-file.jpg")); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, deleteRequest(t, app, "admin", []string{"missing-file"}, true))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("stale metadata delete: %d: %s", w.Code, w.Body.String())
	}
	assertPhotoCount(t, app, 0)
}
