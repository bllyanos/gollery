package main

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func familyCookie(t *testing.T, app *application) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	app.setSession(w, "family")
	return w.Result().Cookies()[0]
}

func downloadRequest(app *application, cookie *http.Cookie, ids ...string) *http.Request {
	form := url.Values{"photo_id": ids}
	if cookie != nil {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(cookie)
		form.Set("csrf", app.sessionCSRF(r, "family"))
	}
	r := httptest.NewRequest(http.MethodPost, "/photos/download", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

func TestBulkDownloadRequiresFamilySessionAndCSRF(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "visible", false)
	cookie := familyCookie(t, app)
	for _, tc := range []struct {
		name    string
		request *http.Request
		want    int
	}{
		{"unauthenticated", downloadRequest(app, nil, "visible"), 401},
		{"missing CSRF", httptest.NewRequest(http.MethodPost, "/photos/download", strings.NewReader("photo_id=visible")), 400},
		{"invalid CSRF", httptest.NewRequest(http.MethodPost, "/photos/download", strings.NewReader("photo_id=visible&csrf=incorrect")), 400},
		{"valid family", downloadRequest(app, cookie, "visible"), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "missing CSRF" || tc.name == "invalid CSRF" {
				tc.request.AddCookie(cookie)
				tc.request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			w := httptest.NewRecorder()
			app.routes().ServeHTTP(w, tc.request)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
	admin := httptest.NewRecorder()
	app.setSession(admin, "admin")
	r := downloadRequest(app, nil, "visible")
	r.AddCookie(admin.Result().Cookies()[0])
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("admin-only status = %d, want 401", w.Code)
	}
}

func TestBulkDownloadRejectsInvalidAndUnavailableSelections(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "visible", false)
	addTestPhoto(t, app, "hidden", true)
	cookie := familyCookie(t, app)
	for _, tc := range []struct {
		name string
		ids  []string
		want int
	}{
		{"empty", nil, 400},
		{"hidden", []string{"visible", "hidden"}, 404},
		{"missing", []string{"visible", "missing"}, 404},
		{"duplicate", []string{"visible", "visible"}, 400},
		{"invalid id", []string{"../visible"}, 400},
		{"over limit", make([]string, maxDownloadSelection+1), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.routes().ServeHTTP(w, downloadRequest(app, cookie, tc.ids...))
			if w.Code != tc.want || w.Header().Get("Content-Type") == "application/zip" {
				t.Fatalf("status = %d, type = %q; want %d and no ZIP", w.Code, w.Header().Get("Content-Type"), tc.want)
			}
		})
	}
	// Authorization must use current database state, not a gallery page's old selection.
	if _, err := app.db.Exec(`UPDATE photos SET hidden = 1 WHERE id = 'visible'`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, downloadRequest(app, cookie, "visible"))
	if w.Code != 404 {
		t.Fatalf("newly hidden photo status = %d, want 404", w.Code)
	}
}

func TestBulkDownloadIncludesVisiblePhotosWithGeneratedNames(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "first", false)
	addTestPhoto(t, app, "second", false)
	if _, err := app.db.Exec(`UPDATE photos SET title = ? WHERE id = 'first'`, "../private/secret"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, downloadRequest(app, familyCookie(t, app), "first", "second"))
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("status = %d, type = %q", w.Code, w.Header().Get("Content-Type"))
	}
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 2 {
		t.Fatalf("ZIP entries = %d, want 2", len(archive.File))
	}
	for i, entry := range archive.File {
		want := []string{"photo-001.jpg", "photo-002.jpg"}[i]
		if entry.Name != want {
			t.Fatalf("entry name = %q, want %q", entry.Name, want)
		}
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil || string(data) != "test image" {
			t.Fatalf("entry content = %q, error = %v", data, err)
		}
	}
}

func TestBulkDownloadRejectsOversizedPayloadBeforeZIPResponse(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "first", false)
	addTestPhoto(t, app, "second", false)
	for _, p := range []struct {
		id   string
		size int64
	}{
		{"first", 60 << 20},
		{"second", 40<<20 + 1},
	} {
		path := filepath.Join(app.cfg.dataDir, "photos", p.id+".jpg")
		if err := os.Truncate(path, p.size); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, downloadRequest(app, familyCookie(t, app), "first", "second"))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got == "application/zip" {
		t.Fatalf("Content-Type = %q, want error response", got)
	}
	if got := w.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("Content-Disposition = %q, want no attachment", got)
	}
	if got := w.Body.String(); got != "Selection exceeds 100 MiB ZIP payload limit\n" {
		t.Fatalf("body = %q, want only size error (no partial ZIP)", got)
	}
}

func TestBulkDownloadBoundsFinalZIPBeforeResponding(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "visible", false)
	cookie := familyCookie(t, app)
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)

	baseline := httptest.NewRecorder()
	app.routes().ServeHTTP(baseline, downloadRequest(app, cookie, "visible"))
	if baseline.Code != http.StatusOK {
		t.Fatalf("baseline status = %d", baseline.Code)
	}
	archiveSize := baseline.Body.Len()
	photoInfo, err := os.Stat(filepath.Join(app.cfg.dataDir, "photos", "visible.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if int64(archiveSize) <= photoInfo.Size() {
		t.Fatalf("archive size = %d, payload size = %d", archiveSize, photoInfo.Size())
	}

	for _, tc := range []struct {
		name  string
		limit int64
		want  int
	}{
		{"one byte below ZIP size", int64(archiveSize - 1), http.StatusRequestEntityTooLarge},
		{"exact ZIP size", int64(archiveSize), http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.downloadPhotosWithLimit(w, downloadRequest(app, cookie, "visible"), tc.limit)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			if tc.want == http.StatusRequestEntityTooLarge {
				if w.Header().Get("Content-Type") == "application/zip" || w.Header().Get("Content-Disposition") != "" {
					t.Fatalf("unexpected ZIP headers: %v", w.Header())
				}
				if got := w.Body.String(); got != "Selection exceeds 100 MiB ZIP archive limit\n" {
					t.Fatalf("error body = %q", got)
				}
			} else if !bytes.Equal(w.Body.Bytes(), baseline.Body.Bytes()) {
				t.Fatal("archive at exact size limit differs from baseline")
			}
			entries, err := os.ReadDir(tempDir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary ZIP files remain: %v, error: %v", entries, err)
			}
		})
	}
}

func TestMediaDownloadMaintainsAccessRules(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "visible", false)
	addTestPhoto(t, app, "hidden", true)
	cookie := familyCookie(t, app)
	for _, tc := range []struct {
		name   string
		id     string
		query  string
		cookie *http.Cookie
		want   int
	}{
		{"anonymous visible", "visible", "?download=1", nil, 404},
		{"family hidden", "hidden", "?download=1", cookie, 404},
		{"family visible", "visible", "?download=1", cookie, 200},
		{"normal media", "visible", "", cookie, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/media/"+tc.id+tc.query, nil)
			if tc.cookie != nil {
				r.AddCookie(tc.cookie)
			}
			w := httptest.NewRecorder()
			app.routes().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			if tc.want == 200 && tc.query != "" && !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
				t.Fatal("visible download did not set attachment disposition")
			}
		})
	}
}
