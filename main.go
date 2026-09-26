package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

//go:embed templates/*.html static/*.js
var templateFiles embed.FS

const maxUploadSize = 15 << 20
const sessionDuration = 7 * 24 * time.Hour

type config struct {
	addr, dataDir, familyPassword, adminUser, adminPassword, sessionKey string
	secureCookies                                                       bool
}

type photo struct {
	ID, Label, CreatedAt string
	Hidden               bool
}

type application struct {
	cfg                   config
	db                    *sql.DB
	familyHash, adminHash []byte
	templates             *template.Template
}

func loadConfig() (config, error) {
	c := config{
		addr: env("GOLLERY_ADDR", ":8080"), dataDir: env("GOLLERY_DATA_DIR", "data"),
		familyPassword: os.Getenv("GOLLERY_FAMILY_PASSWORD"), adminUser: os.Getenv("GOLLERY_ADMIN_USER"),
		adminPassword: os.Getenv("GOLLERY_ADMIN_PASSWORD"), sessionKey: os.Getenv("GOLLERY_SESSION_KEY"),
		secureCookies: strings.EqualFold(os.Getenv("GOLLERY_COOKIE_SECURE"), "true"),
	}
	if len(c.familyPassword) < 8 || len(c.adminPassword) < 12 || c.adminUser == "" {
		return c, errors.New("set GOLLERY_FAMILY_PASSWORD (8+ chars), GOLLERY_ADMIN_USER, and GOLLERY_ADMIN_PASSWORD (12+ chars)")
	}
	if len(c.sessionKey) < 32 {
		return c, errors.New("GOLLERY_SESSION_KEY must contain at least 32 characters of random secret material")
	}
	return c, nil
}

func env(k, fallback string) string {
	if value := os.Getenv(k); value != "" {
		return value
	}
	return fallback
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	app, err := newApplication(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer app.db.Close()
	server := &http.Server{Addr: cfg.addr, Handler: app.routes(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("gollery listening on %s", cfg.addr)
	log.Fatal(server.ListenAndServe())
}

func newApplication(cfg config) (*application, error) {
	if err := os.MkdirAll(filepath.Join(cfg.dataDir, "photos"), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(cfg.dataDir, "gallery.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; CREATE TABLE IF NOT EXISTS photos (
		id TEXT PRIMARY KEY, filename TEXT NOT NULL UNIQUE, label TEXT NOT NULL DEFAULT '',
		hidden INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL
	)`); err != nil {
		db.Close()
		return nil, err
	}
	familyHash, err := bcrypt.GenerateFromPassword([]byte(cfg.familyPassword), bcrypt.DefaultCost)
	if err != nil {
		db.Close()
		return nil, err
	}
	adminHash, err := bcrypt.GenerateFromPassword([]byte(cfg.adminPassword), bcrypt.DefaultCost)
	if err != nil {
		db.Close()
		return nil, err
	}
	templates, err := template.ParseFS(templateFiles, "templates/*.html")
	if err != nil {
		db.Close()
		return nil, err
	}
	return &application{cfg: cfg, db: db, familyHash: familyHash, adminHash: adminHash, templates: templates}, nil
}

func (a *application) routes() http.Handler {
	mux := http.NewServeMux()
	staticFiles, _ := fs.Sub(templateFiles, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))
	mux.HandleFunc("GET /", a.gallery)
	mux.HandleFunc("POST /login", a.familyLogin)
	mux.HandleFunc("POST /logout", a.familyLogout)
	mux.HandleFunc("GET /admin", a.adminPage)
	mux.HandleFunc("POST /admin/login", a.adminLogin)
	mux.HandleFunc("POST /admin/logout", a.adminLogout)
	mux.HandleFunc("POST /admin/photos", a.uploadPhoto)
	mux.HandleFunc("POST /admin/photos/{id}/label", a.updateLabel)
	mux.HandleFunc("POST /admin/photos/{id}/visibility", a.toggleVisibility)
	mux.HandleFunc("POST /admin/photos/{id}/delete", a.deletePhoto)
	mux.HandleFunc("GET /media/{id}", a.servePhoto)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (a *application) gallery(w http.ResponseWriter, r *http.Request) {
	photos := []photo{}
	authenticated := a.validSession(r, "family")
	if authenticated {
		rows, err := a.db.Query(`SELECT id, label, created_at, hidden FROM photos WHERE hidden = 0 ORDER BY created_at DESC`)
		if err != nil {
			http.Error(w, "Unable to load gallery", 500)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var p photo
			if err := rows.Scan(&p.ID, &p.Label, &p.CreatedAt, &p.Hidden); err != nil {
				http.Error(w, "Unable to load gallery", 500)
				return
			}
			photos = append(photos, p)
		}
		if rows.Err() != nil {
			http.Error(w, "Unable to load gallery", 500)
			return
		}
	}
	csrf := a.formToken(w, r, "family-login")
	if authenticated {
		csrf = a.sessionCSRF(r, "family")
	}
	a.render(w, "gallery.html", map[string]any{"Authenticated": authenticated, "Photos": photos, "CSRF": csrf})
}

func (a *application) familyLogin(w http.ResponseWriter, r *http.Request) {
	if !a.checkFormToken(r, "family-login") {
		http.Error(w, "Invalid form", 400)
		return
	}
	if bcrypt.CompareHashAndPassword(a.familyHash, []byte(r.FormValue("password"))) != nil {
		http.Error(w, "Incorrect password", 401)
		return
	}
	a.setSession(w, "family")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *application) familyLogout(w http.ResponseWriter, r *http.Request) {
	if !a.requireSession(w, r, "family") {
		return
	}
	if !a.checkSessionCSRF(r, "family") {
		http.Error(w, "Invalid form", 400)
		return
	}
	a.clearSession(w, "family")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *application) adminPage(w http.ResponseWriter, r *http.Request) {
	if !a.validSession(r, "admin") {
		a.render(w, "admin-login.html", map[string]any{"CSRF": a.formToken(w, r, "admin-login")})
		return
	}
	photos := []photo{}
	rows, err := a.db.Query(`SELECT id, label, created_at, hidden FROM photos ORDER BY created_at DESC`)
	if err != nil {
		http.Error(w, "Unable to load photos", 500)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var p photo
		if err := rows.Scan(&p.ID, &p.Label, &p.CreatedAt, &p.Hidden); err != nil {
			http.Error(w, "Unable to load photos", 500)
			return
		}
		photos = append(photos, p)
	}
	if rows.Err() != nil {
		http.Error(w, "Unable to load photos", 500)
		return
	}
	a.render(w, "admin.html", map[string]any{"Photos": photos, "CSRF": a.sessionCSRF(r, "admin")})
}

func (a *application) adminLogin(w http.ResponseWriter, r *http.Request) {
	if !a.checkFormToken(r, "admin-login") {
		http.Error(w, "Invalid form", 400)
		return
	}
	if r.FormValue("username") != a.cfg.adminUser || bcrypt.CompareHashAndPassword(a.adminHash, []byte(r.FormValue("password"))) != nil {
		http.Error(w, "Incorrect username or password", 401)
		return
	}
	a.setSession(w, "admin")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *application) adminLogout(w http.ResponseWriter, r *http.Request) {
	if !a.requireSession(w, r, "admin") {
		return
	}
	if !a.checkSessionCSRF(r, "admin") {
		http.Error(w, "Invalid form", 400)
		return
	}
	a.clearSession(w, "admin")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *application) uploadPhoto(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeAdminPost(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+(1<<20))
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, "Upload is too large or invalid", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("photo")
	if err != nil {
		http.Error(w, "Choose an image to upload", 400)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxUploadSize+1))
	if err != nil || len(data) == 0 || len(data) > maxUploadSize {
		http.Error(w, "Image must be between 1 byte and 15 MiB", 400)
		return
	}
	contentType := http.DetectContentType(data[:min(len(data), 512)])
	extensions := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp"}
	ext, ok := extensions[contentType]
	if !ok {
		http.Error(w, "Only JPEG, PNG, GIF, and WebP images are allowed", 400)
		return
	}
	id, err := randomID()
	if err != nil {
		http.Error(w, "Unable to store image", 500)
		return
	}
	filename := id + ext
	path := filepath.Join(a.cfg.dataDir, "photos", filename)
	if err := os.WriteFile(path, data, 0600); err != nil {
		http.Error(w, "Unable to store image", 500)
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	if len(label) > 500 {
		os.Remove(path)
		http.Error(w, "Label is too long", 400)
		return
	}
	_, err = a.db.Exec(`INSERT INTO photos(id, filename, label, created_at) VALUES(?, ?, ?, ?)`, id, filename, label, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		os.Remove(path)
		http.Error(w, "Unable to save photo metadata", 500)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *application) updateLabel(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeAdminPost(w, r) {
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	if len(label) > 500 {
		http.Error(w, "Label is too long", 400)
		return
	}
	if _, err := a.db.Exec(`UPDATE photos SET label = ? WHERE id = ?`, label, r.PathValue("id")); err != nil {
		http.Error(w, "Unable to update label", 500)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *application) toggleVisibility(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeAdminPost(w, r) {
		return
	}
	if _, err := a.db.Exec(`UPDATE photos SET hidden = CASE hidden WHEN 0 THEN 1 ELSE 0 END WHERE id = ?`, r.PathValue("id")); err != nil {
		http.Error(w, "Unable to update photo", 500)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *application) deletePhoto(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeAdminPost(w, r) {
		return
	}
	var filename string
	err := a.db.QueryRow(`SELECT filename FROM photos WHERE id = ?`, r.PathValue("id")).Scan(&filename)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Unable to delete photo", 500)
		return
	}
	if err := os.Remove(filepath.Join(a.cfg.dataDir, "photos", filename)); err != nil && !errors.Is(err, os.ErrNotExist) {
		http.Error(w, "Unable to remove photo file", 500)
		return
	}
	if _, err := a.db.Exec(`DELETE FROM photos WHERE id = ?`, r.PathValue("id")); err != nil {
		http.Error(w, "Unable to delete photo metadata", 500)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *application) servePhoto(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var filename string
	var hidden bool
	err := a.db.QueryRow(`SELECT filename, hidden FROM photos WHERE id = ?`, id).Scan(&filename, &hidden)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Unable to load image", 500)
		return
	}
	if a.validSession(r, "admin") { /* Admins can review hidden photos. */
	} else if !a.validSession(r, "family") || hidden {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(a.cfg.dataDir, "photos", filename))
}

func (a *application) authorizeAdminPost(w http.ResponseWriter, r *http.Request) bool {
	if !a.requireSession(w, r, "admin") {
		return false
	}
	if !a.checkSessionCSRF(r, "admin") {
		http.Error(w, "Invalid form", 400)
		return false
	}
	return true
}

func (a *application) requireSession(w http.ResponseWriter, r *http.Request, scope string) bool {
	if a.validSession(r, scope) {
		return true
	}
	http.Error(w, "Authentication required", http.StatusUnauthorized)
	return false
}

func (a *application) validSession(r *http.Request, scope string) bool {
	cookie, err := r.Cookie(scope + "_session")
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(a.cfg.sessionKey))
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	fields := strings.Split(string(payload), ":")
	if len(fields) != 3 || fields[0] != scope {
		return false
	}
	var expiry int64
	if _, err := fmt.Sscan(fields[1], &expiry); err != nil || time.Now().Unix() >= expiry {
		return false
	}
	return len(fields[2]) >= 16
}

func (a *application) sessionCSRF(r *http.Request, scope string) string {
	c, err := r.Cookie(scope + "_session")
	if err != nil {
		return ""
	}
	fields, err := decodeSigned(c.Value, a.cfg.sessionKey)
	if err != nil {
		return ""
	}
	parts := strings.Split(string(fields), ":")
	if len(parts) != 3 || parts[0] != scope {
		return ""
	}
	return parts[2]
}

func (a *application) checkSessionCSRF(r *http.Request, scope string) bool {
	return secureEqual(r.FormValue("csrf"), a.sessionCSRF(r, scope))
}

func (a *application) setSession(w http.ResponseWriter, scope string) {
	nonce, _ := randomID()
	value := signedToken([]byte(fmt.Sprintf("%s:%d:%s", scope, time.Now().Add(sessionDuration).Unix(), nonce)), a.cfg.sessionKey)
	http.SetCookie(w, &http.Cookie{Name: scope + "_session", Value: value, Path: "/", MaxAge: int(sessionDuration.Seconds()), HttpOnly: true, Secure: a.cfg.secureCookies, SameSite: http.SameSiteStrictMode})
}

func (a *application) clearSession(w http.ResponseWriter, scope string) {
	http.SetCookie(w, &http.Cookie{Name: scope + "_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cfg.secureCookies, SameSite: http.SameSiteStrictMode})
}

func (a *application) formToken(w http.ResponseWriter, r *http.Request, purpose string) string {
	name := purpose + "_csrf"
	c, err := r.Cookie(name)
	if err == nil {
		if value, err := decodeSigned(c.Value, a.cfg.sessionKey); err == nil && string(value) != "" {
			return string(value)
		}
	}
	nonce, _ := randomID()
	token := signedToken([]byte(nonce), a.cfg.sessionKey)
	http.SetCookie(w, &http.Cookie{Name: name, Value: token, Path: "/", MaxAge: 600, HttpOnly: true, Secure: a.cfg.secureCookies, SameSite: http.SameSiteStrictMode})
	return nonce
}

func (a *application) checkFormToken(r *http.Request, purpose string) bool {
	c, err := r.Cookie(purpose + "_csrf")
	if err != nil {
		return false
	}
	value, err := decodeSigned(c.Value, a.cfg.sessionKey)
	return err == nil && secureEqual(r.FormValue("csrf"), string(value))
}

func signedToken(payload []byte, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func decodeSigned(value, key string) ([]byte, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, errors.New("invalid signature")
	}
	return payload, nil
}

func secureEqual(a, b string) bool { return a != "" && b != "" && hmac.Equal([]byte(a), []byte(b)) }

func randomID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (a *application) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}
