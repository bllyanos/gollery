package main

import (
	"archive/zip"
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

//go:embed templates/*.html static/*.js static/*.css
var templateFiles embed.FS

const maxUploadSize = 15 << 20
const maxBulkUploadCount = 10
const maxBulkImageBytes = 60 << 20
const maxBulkRequestBytes = maxBulkImageBytes + (1 << 20)
const sessionDuration = 7 * 24 * time.Hour
const maxDownloadSelection = 50
const maxDeleteSelection = 50
const maxZipPayloadSize int64 = 100 << 20

type config struct {
	addr, dataDir, familyPassword, adminUser, adminPassword, sessionKey string
	secureCookies                                                       bool
}

type photo struct {
	ID, Title, CreatedAt string
	Tags                 []string
	TagsText             string
	Hidden               bool
}

type application struct {
	cfg                   config
	db                    *sql.DB
	familyHash, adminHash []byte
	templates             *template.Template
	uploadNotices         struct {
		sync.Mutex
		expires map[string]int64
	}
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
	if err := loadDotEnv(".env"); err != nil {
		log.Fatal(err)
	}
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

// loadDotEnv loads KEY=VALUE settings from a local .env file without replacing
// values already provided by the process environment. A missing file is fine.
func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return fmt.Errorf("parse %s:%d: expected KEY=VALUE", path, lineNumber)
		}
		if strings.HasPrefix(value, `"`) || strings.HasSuffix(value, `"`) {
			if len(value) < 2 || !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `"`) {
				return fmt.Errorf("parse %s:%d: unmatched double quote", path, lineNumber)
			}
			value, err = strconv.Unquote(value)
			if err != nil {
				return fmt.Errorf("parse %s:%d: %w", path, lineNumber, err)
			}
		} else if strings.HasPrefix(value, "'") || strings.HasSuffix(value, "'") {
			if len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
				return fmt.Errorf("parse %s:%d: unmatched single quote", path, lineNumber)
			}
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("set %s from %s:%d: %w", key, path, lineNumber, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
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
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateDatabase(db); err != nil {
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
	templates, err := template.New("").Funcs(template.FuncMap{
		"tagKey": tagKey,
		"jsonTags": func(tags []string) string {
			keys := make([]string, len(tags))
			for i, tag := range tags {
				keys[i] = tagKey(tag)
			}
			data, _ := json.Marshal(keys)
			return string(data)
		},
	}).ParseFS(templateFiles, "templates/*.html")
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
	mux.HandleFunc("GET /admin/bulk-upload", a.bulkUploadPage)
	mux.HandleFunc("POST /admin/bulk-upload", a.bulkUploadPhotos)
	mux.HandleFunc("POST /admin/login", a.adminLogin)
	mux.HandleFunc("POST /admin/logout", a.adminLogout)
	mux.HandleFunc("POST /admin/photos", a.uploadPhoto)
	mux.HandleFunc("POST /admin/photos/delete", a.deleteSelectedPhotos)
	mux.HandleFunc("POST /admin/photos/{id}/title", a.updateTitle)
	mux.HandleFunc("POST /admin/photos/{id}/tags", a.updateTags)
	mux.HandleFunc("POST /admin/photos/{id}/visibility", a.toggleVisibility)
	mux.HandleFunc("POST /admin/photos/{id}/delete", a.deletePhoto)
	mux.HandleFunc("GET /media/{id}", a.servePhoto)
	mux.HandleFunc("POST /photos/download", a.downloadPhotos)
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
	authenticated := a.validSession(r, "family")
	photos := []photo{}
	tags := []string{}
	if authenticated {
		var err error
		photos, err = a.loadPhotos(false)
		if err != nil {
			http.Error(w, "Unable to load gallery", 500)
			return
		}
		seen := map[string]bool{}
		for _, p := range photos {
			for _, tag := range p.Tags {
				if !seen[tag] {
					seen[tag] = true
					tags = append(tags, tag)
				}
			}
		}
		sort.Slice(tags, func(i, j int) bool { return tagKey(tags[i]) < tagKey(tags[j]) })
	}
	csrf := a.formToken(w, r, "family-login")
	if authenticated {
		csrf = a.sessionCSRF(r, "family")
	}
	a.render(w, "gallery.html", map[string]any{"Authenticated": authenticated, "Photos": photos, "Tags": tags, "CSRF": csrf})
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
	photos, err := a.loadPhotos(true)
	if err != nil {
		http.Error(w, "Unable to load photos", 500)
		return
	}
	a.render(w, "admin.html", map[string]any{"Photos": photos, "CSRF": a.sessionCSRF(r, "admin")})
}

func (a *application) bulkUploadPage(w http.ResponseWriter, r *http.Request) {
	if !a.requireSession(w, r, "admin") {
		return
	}
	uploaded := a.consumeUploadNotice(w, r)
	a.render(w, "bulk-upload.html", map[string]any{"CSRF": a.sessionCSRF(r, "admin"), "Uploaded": uploaded})
}

func (a *application) consumeUploadNotice(w http.ResponseWriter, r *http.Request) int {
	name := "bulk_upload_notice"
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/admin/bulk-upload", MaxAge: -1, HttpOnly: true, Secure: a.cfg.secureCookies, SameSite: http.SameSiteStrictMode})
	cookie, err := r.Cookie(name)
	if err != nil {
		return 0
	}
	payload, err := decodeSigned(cookie.Value, a.cfg.sessionKey)
	if err != nil {
		return 0
	}
	parts := strings.Split(string(payload), ":")
	if len(parts) != 4 || parts[0] != a.sessionCSRF(r, "admin") {
		return 0
	}
	count, err := strconv.Atoi(parts[1])
	expiry, expiryErr := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || expiryErr != nil || count < 1 || count > maxBulkUploadCount || parts[3] == "" {
		return 0
	}
	a.uploadNotices.Lock()
	defer a.uploadNotices.Unlock()
	now := time.Now().Unix()
	a.cleanUploadNotices(now)
	if storedExpiry, ok := a.uploadNotices.expires[parts[3]]; !ok || storedExpiry != expiry || now >= expiry {
		return 0
	}
	delete(a.uploadNotices.expires, parts[3])
	return count
}

// cleanUploadNotices requires the uploadNotices lock.
func (a *application) cleanUploadNotices(now int64) {
	for nonce, expiry := range a.uploadNotices.expires {
		if now >= expiry {
			delete(a.uploadNotices.expires, nonce)
		}
	}
}

func (a *application) setUploadNotice(w http.ResponseWriter, r *http.Request, count int) error {
	for {
		nonce, err := randomID()
		if err != nil {
			return err
		}
		a.uploadNotices.Lock()
		now := time.Now().Unix()
		a.cleanUploadNotices(now)
		if a.uploadNotices.expires == nil {
			a.uploadNotices.expires = make(map[string]int64)
		}
		if _, exists := a.uploadNotices.expires[nonce]; exists {
			a.uploadNotices.Unlock()
			continue
		}
		expiry := now + 300
		a.uploadNotices.expires[nonce] = expiry
		a.uploadNotices.Unlock()
		notice := fmt.Sprintf("%s:%d:%d:%s", a.sessionCSRF(r, "admin"), count, expiry, nonce)
		http.SetCookie(w, &http.Cookie{Name: "bulk_upload_notice", Value: signedToken([]byte(notice), a.cfg.sessionKey), Path: "/admin/bulk-upload", MaxAge: 300, HttpOnly: true, Secure: a.cfg.secureCookies, SameSite: http.SameSiteStrictMode})
		return nil
	}
}

type uploadImage struct {
	data []byte
	ext  string
}

func validateUploadImage(data []byte) (string, error) {
	if len(data) == 0 || len(data) > maxUploadSize {
		return "", errors.New("image must be between 1 byte and 15 MiB")
	}
	switch http.DetectContentType(data[:min(len(data), 512)]) {
	case "image/jpeg":
		return ".jpg", nil
	case "image/png":
		return ".png", nil
	case "image/gif":
		return ".gif", nil
	case "image/webp":
		return ".webp", nil
	default:
		return "", errors.New("only JPEG, PNG, GIF, and WebP images are allowed")
	}
}

func (a *application) bulkUploadPhotos(w http.ResponseWriter, r *http.Request) {
	if !a.requireSession(w, r, "admin") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBulkRequestBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "Invalid upload or request exceeds 61 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	defer r.MultipartForm.RemoveAll()
	if !secureEqual(r.PostForm.Get("csrf"), a.sessionCSRF(r, "admin")) {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	files := r.MultipartForm.File["photos"]
	if len(files) == 0 || len(files) > maxBulkUploadCount {
		http.Error(w, "Choose between 1 and 10 images", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.PostForm.Get("title"))
	if len(title) > 500 {
		http.Error(w, "Title is too long", http.StatusBadRequest)
		return
	}
	images := make([]uploadImage, 0, len(files))
	var total int64
	for _, header := range files {
		if header.Size > maxUploadSize || header.Size > maxBulkImageBytes-total {
			http.Error(w, "Images must be at most 15 MiB each and 60 MiB together", http.StatusRequestEntityTooLarge)
			return
		}
		file, err := header.Open()
		if err != nil {
			http.Error(w, "Unable to read image", http.StatusBadRequest)
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxUploadSize+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			http.Error(w, "Unable to read image", http.StatusBadRequest)
			return
		}
		ext, err := validateUploadImage(data)
		if err != nil {
			http.Error(w, fmt.Sprintf("Image %d: %s", len(images)+1, err), http.StatusBadRequest)
			return
		}
		total += int64(len(data))
		if total > maxBulkImageBytes {
			http.Error(w, "Images exceed 60 MiB together", http.StatusRequestEntityTooLarge)
			return
		}
		images = append(images, uploadImage{data: data, ext: ext})
	}
	if err := a.storeBatch(images, title); err != nil {
		log.Printf("bulk upload: %v", err)
		http.Error(w, "Unable to store images", http.StatusInternalServerError)
		return
	}
	if err := a.setUploadNotice(w, r, len(images)); err != nil {
		log.Printf("bulk upload notice: %v", err)
	}
	http.Redirect(w, r, "/admin/bulk-upload", http.StatusSeeOther)
}

// Keep new files invisible to readers until all inserts commit. On any failure,
// roll back metadata and remove every file created by this batch.
func (a *application) storeBatch(images []uploadImage, title string) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	paths := make([]string, 0, len(images))
	defer func() {
		if !committed {
			tx.Rollback()
			for _, path := range paths {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					log.Printf("clean up bulk upload %s: %v", path, err)
				}
			}
		}
	}()
	for _, image := range images {
		id, err := randomID()
		if err != nil {
			return err
		}
		filename := id + image.ext
		path := filepath.Join(a.cfg.dataDir, "photos", filename)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		paths = append(paths, path)
		n, writeErr := file.Write(image.data)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if n != len(image.data) {
			return io.ErrShortWrite
		}
		if closeErr != nil {
			return closeErr
		}
		if _, err := tx.Exec(`INSERT INTO photos(id, filename, title, created_at) VALUES(?, ?, ?, ?)`, id, filename, title, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
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
	if err != nil {
		http.Error(w, "Image must be between 1 byte and 15 MiB", 400)
		return
	}
	ext, err := validateUploadImage(data)
	if err != nil {
		http.Error(w, err.Error(), 400)
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
	title := strings.TrimSpace(r.FormValue("title"))
	if len(title) > 500 {
		os.Remove(path)
		http.Error(w, "Title is too long", 400)
		return
	}
	_, err = a.db.Exec(`INSERT INTO photos(id, filename, title, created_at) VALUES(?, ?, ?, ?)`, id, filename, title, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		os.Remove(path)
		http.Error(w, "Unable to save photo metadata", 500)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *application) updateTitle(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeAdminPost(w, r) {
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if len(title) > 500 {
		http.Error(w, "Title is too long", 400)
		return
	}
	if _, err := a.db.Exec(`UPDATE photos SET title = ? WHERE id = ?`, title, r.PathValue("id")); err != nil {
		http.Error(w, "Unable to update title", 500)
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
	if !safePhotoID(r.PathValue("id")) {
		http.NotFound(w, r)
		return
	}
	if err := a.removePhotos([]string{r.PathValue("id")}); err != nil {
		a.deleteError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *application) deleteSelectedPhotos(w http.ResponseWriter, r *http.Request) {
	if !a.requireSession(w, r, "admin") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid selection", http.StatusBadRequest)
		return
	}
	if !secureEqual(r.PostForm.Get("csrf"), a.sessionCSRF(r, "admin")) {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	ids := r.PostForm["photo_id"]
	if len(ids) == 0 || len(ids) > maxDeleteSelection {
		http.Error(w, "Select between 1 and 50 photos", http.StatusBadRequest)
		return
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !safePhotoID(id) || seen[id] {
			http.Error(w, "Invalid selection", http.StatusBadRequest)
			return
		}
		seen[id] = true
	}
	if err := a.removePhotos(ids); err != nil {
		a.deleteError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

var errPhotoMissing = errors.New("photo not found")
var errUnsafePhoto = errors.New("unsafe photo filename")

type stagedDeletion struct{ original, staged string }

// Move files out of the served namespace before committing metadata deletion.
// A failed lookup, move, or DB operation restores every moved file and rolls back.
func (a *application) removePhotos(ids []string) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	staged := []stagedDeletion{}
	defer func() {
		if !committed {
			_ = tx.Rollback()
			for i := len(staged) - 1; i >= 0; i-- {
				if err := os.Rename(staged[i].staged, staged[i].original); err != nil {
					log.Printf("restore photo after failed deletion: %v", err)
				}
			}
		}
	}()
	paths := make([]string, 0, len(ids))
	for _, id := range ids {
		var filename string
		if err := tx.QueryRow(`SELECT filename FROM photos WHERE id = ?`, id).Scan(&filename); errors.Is(err, sql.ErrNoRows) {
			return errPhotoMissing
		} else if err != nil {
			return err
		}
		if !safePhotoFilename(id, filename) {
			return errUnsafePhoto
		}
		paths = append(paths, filepath.Join(a.cfg.dataDir, "photos", filename))
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) { // Stale metadata can still be removed.
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errUnsafePhoto
		}
		nonce, err := randomID()
		if err != nil {
			return err
		}
		staging := filepath.Join(a.cfg.dataDir, "photos", ".delete-"+nonce)
		if err := os.Rename(path, staging); err != nil {
			return err
		}
		staged = append(staged, stagedDeletion{path, staging})
	}
	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM photos WHERE id = ?`, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM tags WHERE NOT EXISTS (SELECT 1 FROM photo_tags WHERE tag_id = tags.id)`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	for _, item := range staged {
		if err := os.Remove(item.staged); err != nil {
			log.Printf("remove staged photo %s: %v", item.staged, err)
			return err
		}
	}
	return nil
}

func (a *application) deleteError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errPhotoMissing) {
		http.NotFound(w, r)
	} else {
		log.Printf("delete photos: %v", err)
		http.Error(w, "Unable to delete photos", http.StatusInternalServerError)
	}
}

func (a *application) servePhoto(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
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
	if !safePhotoFilename(id, filename) {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Has("download") {
		ext := filepath.Ext(filename)
		w.Header().Set("Content-Disposition", `attachment; filename="photo`+ext+`"`)
	}
	http.ServeFile(w, r, filepath.Join(a.cfg.dataDir, "photos", filename))
}

type selectedPhoto struct {
	file *os.File
	ext  string
	size int64
}

var errArchiveTooLarge = errors.New("ZIP archive exceeds size limit")

type boundedArchiveWriter struct {
	file      *os.File
	remaining int64
}

func (b *boundedArchiveWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > b.remaining {
		n, err := b.file.Write(p[:int(b.remaining)])
		b.remaining -= int64(n)
		if err != nil {
			return n, err
		}
		return n, errArchiveTooLarge
	}
	n, err := b.file.Write(p)
	b.remaining -= int64(n)
	return n, err
}

func writePhotoArchive(file *os.File, selected []selectedPhoto, limit int64) error {
	archive := zip.NewWriter(&boundedArchiveWriter{file: file, remaining: limit})
	for i, p := range selected {
		entry, err := archive.Create(fmt.Sprintf("photo-%03d%s", i+1, p.ext))
		if err != nil {
			return err
		}
		if _, err := io.CopyN(entry, p.file, p.size); err != nil {
			return err
		}
	}
	return archive.Close()
}

// Downloads are validated and fully assembled before response headers are written.
// A photo hidden since the gallery was rendered must fail the entire request.
func (a *application) downloadPhotos(w http.ResponseWriter, r *http.Request) {
	a.downloadPhotosWithLimit(w, r, maxZipPayloadSize)
}

func (a *application) downloadPhotosWithLimit(w http.ResponseWriter, r *http.Request, limit int64) {
	if !a.requireSession(w, r, "family") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid selection", http.StatusBadRequest)
		return
	}
	if !secureEqual(r.PostForm.Get("csrf"), a.sessionCSRF(r, "family")) {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	ids := r.PostForm["photo_id"]
	if len(ids) == 0 || len(ids) > maxDownloadSelection {
		http.Error(w, "Select between 1 and 50 photos", http.StatusBadRequest)
		return
	}
	selected := make([]selectedPhoto, 0, len(ids))
	defer func() {
		for _, p := range selected {
			p.file.Close()
		}
	}()
	seen := make(map[string]bool, len(ids))
	var totalSize int64
	for _, id := range ids {
		if seen[id] || !safePhotoID(id) {
			http.Error(w, "Invalid selection", http.StatusBadRequest)
			return
		}
		seen[id] = true
		var filename string
		var hidden bool
		err := a.db.QueryRow(`SELECT filename, hidden FROM photos WHERE id = ?`, id).Scan(&filename, &hidden)
		if errors.Is(err, sql.ErrNoRows) || hidden || (err == nil && !safePhotoFilename(id, filename)) {
			http.Error(w, "Photo not available", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "Unable to prepare download", http.StatusInternalServerError)
			return
		}
		file, err := os.Open(filepath.Join(a.cfg.dataDir, "photos", filename))
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "Photo not available", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "Unable to prepare download", http.StatusInternalServerError)
			return
		}
		selected = append(selected, selectedPhoto{file: file, ext: filepath.Ext(filename)})
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.Error(w, "Unable to prepare download", http.StatusInternalServerError)
			return
		}
		if info.Size() > limit-totalSize {
			http.Error(w, "Selection exceeds 100 MiB ZIP payload limit", http.StatusRequestEntityTooLarge)
			return
		}
		totalSize += info.Size()
		selected[len(selected)-1].size = info.Size()
	}
	file, err := os.CreateTemp("", "gollery-download-*.zip")
	if err != nil {
		http.Error(w, "Unable to prepare download", http.StatusInternalServerError)
		return
	}
	defer func() {
		file.Close()
		os.Remove(file.Name())
	}()
	if err := writePhotoArchive(file, selected, limit); err != nil {
		if errors.Is(err, errArchiveTooLarge) {
			http.Error(w, "Selection exceeds 100 MiB ZIP archive limit", http.StatusRequestEntityTooLarge)
		} else {
			log.Printf("prepare ZIP: %v", err)
			http.Error(w, "Unable to prepare download", http.StatusInternalServerError)
		}
		return
	}
	info, err := file.Stat()
	if err != nil || info.Size() > limit {
		http.Error(w, "Unable to prepare download", http.StatusInternalServerError)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		http.Error(w, "Unable to prepare download", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="family-photos.zip"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if _, err := io.Copy(w, file); err != nil {
		log.Printf("send ZIP: %v", err)
	}
}

func safePhotoID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func safePhotoFilename(id, filename string) bool {
	ext := filepath.Ext(filename)
	switch ext {
	case ".jpg", ".png", ".gif", ".webp":
		return safePhotoID(id) && filename == id+ext
	}
	return false
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
