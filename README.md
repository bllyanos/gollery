# Gollery

A small password-protected family photo gallery with a separate admin area. It uses Go's `net/http`, SQLite for photo metadata, and local files for image data.

## Requirements

- Go 1.27.1 or later
- A filesystem location writable by the application

SQLite is provided by [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite), an established pure-Go SQLite driver. Passwords are hashed in memory at startup with bcrypt from `golang.org/x/crypto`; plaintext credentials are not written to the database.

## Configure and run

For local development, copy the example environment file and start the app:

```sh
cp .env.example .env
go run .
```

The application loads `.env` from its current working directory automatically, without replacing variables already set in the process environment. The example credentials and signing key are for local development only. Replace them with unique secrets before any shared or production use. `.env` is optional when configuring the process environment another way.

Required settings:

| Variable | Description |
| --- | --- |
| `GOLLERY_FAMILY_PASSWORD` | Shared gallery password, at least 8 characters. |
| `GOLLERY_ADMIN_USER` | Admin username. |
| `GOLLERY_ADMIN_PASSWORD` | Admin password, at least 12 characters. |
| `GOLLERY_SESSION_KEY` | Random secret used to sign sessions, at least 32 characters. Keep it private and stable between restarts. |

Optional settings:

| Variable | Default | Description |
| --- | --- | --- |
| `GOLLERY_ADDR` | `:8080` | HTTP listen address. |
| `GOLLERY_DATA_DIR` | `data` | Directory for `gallery.db` and the `photos/` files. |
| `GOLLERY_COOKIE_SECURE` | `false` | Set to `true` when serving through HTTPS. |

For production, terminate HTTPS and set `GOLLERY_COOKIE_SECURE=true`. Keep the data directory and environment secrets private, and back up the SQLite database together with the `photos/` directory. The server intentionally has no default credentials and exits if required secrets are missing or too short.

Open `/` for the family password gate and `/admin` for the separate admin sign-in. Admins can upload JPEG, PNG, GIF, and WebP images (up to 15 MiB each), edit titles and tags, hide/show, and delete photos from a responsive list. Enter comma-separated tags per photo (up to 10, 40 characters each); matching names are reused case-insensitively. Save an empty tag field to clear tags. Select 1–50 photos (including hidden photos) and use **Delete selected** to remove them together. Individual and selected deletions ask for browser confirmation; the server requires an admin session and CSRF token and rejects invalid, duplicate, or missing selections before making changes.

From the dashboard, use **Upload multiple photos** for the separate admin-only `GET /admin/bulk-upload` page. Its `POST /admin/bulk-upload` form requires the admin session and CSRF token. Select 1–10 images (15 MiB per image, 60 MiB combined image data, and 61 MiB maximum multipart request including fields). An optional common title applies to every image; otherwise titles are blank. The full batch is checked before storage; an invalid image or storage failure rejects the batch without leaving partial photo records or files. A successful upload redirects back to the batch page with a short-lived signed confirmation cookie and a link to `/admin`; URL parameters cannot forge the notice.

In the family gallery, use the All/tag buttons to filter already-loaded visible photos in the browser, without a filter request. Open a thumbnail for a full-size preview and a single-photo download, or select up to 50 visible photos and download them in one ZIP. The ZIP uses generated entry names rather than photo titles. Downloads require a current family session; the server rechecks photo visibility for every request and rejects an entire selection if a photo was hidden or removed. Existing databases automatically migrate the old photo `label` column to `title` without dropping stored values. Hidden photo metadata and media are never rendered in the family gallery.

Optional landscape sample photos for manually populating the gallery are in [`examples/photos/`](examples/photos/README.md). They are not loaded automatically; see that directory's README for upload instructions and image credits.

## Verify

```sh
gofmt -w *.go
go test ./...
go vet ./...
git diff --check
```
