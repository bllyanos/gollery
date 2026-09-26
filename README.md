# Gollery

A small password-protected family photo gallery with a separate admin area. It uses Go's `net/http`, SQLite for photo metadata, and local files for image data.

## Requirements

- Go 1.27.1 or later
- A filesystem location writable by the application

SQLite is provided by [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite), an established pure-Go SQLite driver. Passwords are hashed in memory at startup with bcrypt from `golang.org/x/crypto`; plaintext credentials are not written to the database.

## Configure and run

Set the required credentials and a random session-signing key before starting the server:

```sh
export GOLLERY_FAMILY_PASSWORD='a-long-shared-family-password'
export GOLLERY_ADMIN_USER='gallery-admin'
export GOLLERY_ADMIN_PASSWORD='a-long-unique-admin-password'
export GOLLERY_SESSION_KEY="$(openssl rand -hex 32)"
go run .
```

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

Open `/` for the family password gate and `/admin` for the separate admin sign-in. Admins can upload JPEG, PNG, GIF, and WebP images (up to 15 MiB each), edit labels, hide/show, and delete photos. Deletion asks for browser confirmation.

## Verify

```sh
gofmt -w main.go main_test.go
go test ./...
```
