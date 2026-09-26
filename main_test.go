package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testApplication(t *testing.T) *application {
	t.Helper()
	dir := t.TempDir()
	app, err := newApplication(config{addr: ":0", dataDir: dir, familyPassword: "family-password", adminUser: "admin", adminPassword: "admin-password", sessionKey: strings.Repeat("k", 32)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.db.Close() })
	return app
}

func addTestPhoto(t *testing.T, app *application, id string, hidden bool) {
	t.Helper()
	name := id + ".jpg"
	if err := os.WriteFile(filepath.Join(app.cfg.dataDir, "photos", name), []byte("test image"), 0600); err != nil {
		t.Fatal(err)
	}
	var flag int
	if hidden {
		flag = 1
	}
	if _, err := app.db.Exec(`INSERT INTO photos(id, filename, label, hidden, created_at) VALUES(?, ?, ?, ?, ?)`, id, name, "test", flag, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
}

func TestFamilyCannotFetchHiddenPhotoByURL(t *testing.T) {
	app := testApplication(t)
	addTestPhoto(t, app, "hidden-photo", true)

	request := httptest.NewRequest("GET", "/media/hidden-photo", nil)
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != 404 {
		t.Fatalf("unauthenticated request status = %d, want 404", recorder.Code)
	}

	request = httptest.NewRequest("GET", "/media/hidden-photo", nil)
	response := httptest.NewRecorder()
	app.setSession(response, "family")
	for _, cookie := range response.Result().Cookies() {
		request.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != 404 {
		t.Fatalf("family request status = %d, want 404", recorder.Code)
	}

	request = httptest.NewRequest("GET", "/media/hidden-photo", nil)
	response = httptest.NewRecorder()
	app.setSession(response, "admin")
	for _, cookie := range response.Result().Cookies() {
		request.AddCookie(cookie)
	}
	if !app.validSession(request, "admin") {
		t.Fatal("generated admin session was not valid")
	}
	recorder = httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("admin request status = %d, want 200", recorder.Code)
	}
}

func TestFamilySessionDoesNotAuthenticateAdmin(t *testing.T) {
	app := testApplication(t)
	request := httptest.NewRequest("GET", "/admin", nil)
	response := httptest.NewRecorder()
	app.setSession(response, "family")
	for _, cookie := range response.Result().Cookies() {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("admin login page status = %d, want 200", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "Upload photo") {
		t.Fatal("family session unexpectedly opened the admin dashboard")
	}
}

func TestGalleryPromptsWithoutFamilySession(t *testing.T) {
	app := testApplication(t)
	request := httptest.NewRequest("GET", "/", nil)
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("gallery status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Family access") {
		t.Fatal("gallery did not show the password gate")
	}
}
