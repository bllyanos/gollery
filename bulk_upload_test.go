package main

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var tinyPNG = []byte("\x89PNG\r\n\x1a\nexample")

func bulkRequest(t *testing.T, app *application, files [][]byte, csrf string, scope string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("csrf", csrf); err != nil {
		t.Fatal(err)
	}
	for i, data := range files {
		part, err := form.CreateFormFile("photos", fmt.Sprintf("untrusted-%d.png", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/admin/bulk-upload", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	if scope != "" {
		response := httptest.NewRecorder()
		app.setSession(response, scope)
		for _, cookie := range response.Result().Cookies() {
			r.AddCookie(cookie)
		}
	}
	return r
}

func adminBulkRequest(t *testing.T, app *application, files [][]byte) *http.Request {
	t.Helper()
	return bulkRequest(t, app, files, "", "admin")
}

func submitBatch(t *testing.T, app *application, files [][]byte) *httptest.ResponseRecorder {
	t.Helper()
	base := adminBulkRequest(t, app, nil)
	r := bulkRequest(t, app, files, app.sessionCSRF(base, "admin"), "")
	for _, cookie := range base.Cookies() {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	return w
}

func assertPhotoCount(t *testing.T, app *application, want int) {
	t.Helper()
	var count int
	if err := app.db.QueryRow(`SELECT count(*) FROM photos`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("database photos = %d, want %d", count, want)
	}
	entries, err := os.ReadDir(filepath.Join(app.cfg.dataDir, "photos"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != want {
		t.Fatalf("stored files = %d, want %d", len(entries), want)
	}
}

func TestBulkUploadRequiresAdminAndCSRF(t *testing.T) {
	app := testApplication(t)
	for _, scope := range []string{"", "family"} {
		r := httptest.NewRequest("GET", "/admin/bulk-upload", nil)
		if scope != "" {
			w := httptest.NewRecorder()
			app.setSession(w, scope)
			for _, c := range w.Result().Cookies() {
				r.AddCookie(c)
			}
		}
		w := httptest.NewRecorder()
		app.routes().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("GET with %q session: %d", scope, w.Code)
		}
		w = httptest.NewRecorder()
		app.routes().ServeHTTP(w, bulkRequest(t, app, [][]byte{tinyPNG}, "", scope))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("POST with %q session: %d", scope, w.Code)
		}
	}
	r := adminBulkRequest(t, app, [][]byte{tinyPNG})
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST without CSRF: %d", w.Code)
	}
	assertPhotoCount(t, app, 0)
}

func TestAdminDashboardKeepsSingleUploadAndLinksToBulk(t *testing.T) {
	app := testApplication(t)
	r := httptest.NewRequest("GET", "/admin", nil)
	w := httptest.NewRecorder()
	app.setSession(w, "admin")
	for _, cookie := range w.Result().Cookies() {
		r.AddCookie(cookie)
	}
	w = httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `action="/admin/photos"`) || !strings.Contains(w.Body.String(), `href="/admin/bulk-upload"`) {
		t.Fatalf("admin dashboard missing upload actions: %d", w.Code)
	}
}

func TestBulkUploadSuccess(t *testing.T) {
	app := testApplication(t)
	base := adminBulkRequest(t, app, nil)
	r := bulkRequest(t, app, [][]byte{tinyPNG, tinyPNG}, app.sessionCSRF(base, "admin"), "")
	for _, cookie := range base.Cookies() {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/bulk-upload" {
		t.Fatalf("upload: %d, location %q: %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	assertPhotoCount(t, app, 2)
	rows, err := app.db.Query(`SELECT id, filename FROM photos`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, filename string
		if err := rows.Scan(&id, &filename); err != nil {
			t.Fatal(err)
		}
		if !safePhotoFilename(id, filename) || strings.Contains(filename, "untrusted") {
			t.Fatalf("unsafe stored name %q", filename)
		}
		data, err := os.ReadFile(filepath.Join(app.cfg.dataDir, "photos", filename))
		if err != nil || !bytes.Equal(data, tinyPNG) {
			t.Fatalf("file %s: %v", filename, err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "/admin/bulk-upload?uploaded=999", nil)
	for _, cookie := range base.Cookies() {
		r.AddCookie(cookie)
	}
	for _, cookie := range w.Result().Cookies() {
		r.AddCookie(cookie)
	}
	w = httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Successfully uploaded 2 photos") || !strings.Contains(w.Body.String(), `href="/admin"`) {
		t.Fatalf("feedback page: %d: %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("GET", "/admin/bulk-upload?uploaded=2", nil)
	for _, cookie := range base.Cookies() {
		r.AddCookie(cookie)
	}
	w = httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "Successfully uploaded") {
		t.Fatal("query parameter forged upload success")
	}
}

func TestBulkUploadNoticeCannotBeReplayed(t *testing.T) {
	app := testApplication(t)
	app.cfg.secureCookies = true
	base := adminBulkRequest(t, app, nil)
	r := bulkRequest(t, app, [][]byte{tinyPNG}, app.sessionCSRF(base, "admin"), "")
	for _, cookie := range base.Cookies() {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/bulk-upload" {
		t.Fatalf("upload: %d, location %q", w.Code, w.Header().Get("Location"))
	}
	var notice *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "bulk_upload_notice" {
			notice = cookie
		}
	}
	if notice == nil || !notice.HttpOnly || !notice.Secure || notice.SameSite != http.SameSiteStrictMode {
		t.Fatalf("missing or insecure notice cookie: %+v", notice)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		r = httptest.NewRequest("GET", "/admin/bulk-upload", nil)
		for _, cookie := range base.Cookies() {
			r.AddCookie(cookie)
		}
		// Replay the original signed cookie even after the response deletes it.
		r.AddCookie(notice)
		w = httptest.NewRecorder()
		app.routes().ServeHTTP(w, r)
		got := strings.Contains(w.Body.String(), "Successfully uploaded 1 photo")
		if w.Code != http.StatusOK || got != (attempt == 1) {
			t.Fatalf("notice attempt %d: status %d, success shown %t", attempt, w.Code, got)
		}
	}
}

func TestBulkUploadInvalidImageIsAtomic(t *testing.T) {
	app := testApplication(t)
	w := submitBatch(t, app, [][]byte{tinyPNG, []byte("not an image")})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid batch: %d: %s", w.Code, w.Body.String())
	}
	assertPhotoCount(t, app, 0)
}

func TestBulkUploadLimits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files [][]byte
		code  int
	}{
		{"count", bytesFiles(maxBulkUploadCount+1, tinyPNG), http.StatusBadRequest},
		{"single file", [][]byte{append(append([]byte{}, tinyPNG...), make([]byte, maxUploadSize)...)}, http.StatusRequestEntityTooLarge},
		{"aggregate", bytesFiles(5, append(append([]byte{}, tinyPNG...), make([]byte, 12<<20)...)), http.StatusRequestEntityTooLarge},
		{"request body", bytesFiles(5, append(append([]byte{}, tinyPNG...), make([]byte, 13<<20)...)), http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApplication(t)
			w := submitBatch(t, app, tc.files)
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.code, w.Body.String())
			}
			assertPhotoCount(t, app, 0)
		})
	}
}

func bytesFiles(n int, data []byte) [][]byte {
	files := make([][]byte, n)
	for i := range files {
		files[i] = data
	}
	return files
}

func TestBulkUploadDatabaseFailureRemovesFiles(t *testing.T) {
	app := testApplication(t)
	if _, err := app.db.Exec(`CREATE TRIGGER fail_second BEFORE INSERT ON photos WHEN (SELECT count(*) FROM photos) > 0 BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	w := submitBatch(t, app, [][]byte{tinyPNG, tinyPNG})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	assertPhotoCount(t, app, 0)
}
