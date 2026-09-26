# Repository guidance

- Write repository files in English; communicate with the user in Indonesian.

## Current state

- `go.mod` declares module `github.com/bllyanos/gollery` and Go `1.27.1`.
- This is currently a documentation-only scaffold: there are no Go packages or tests, and no README, CI, or build/test/lint scripts. `go list ./...` currently matches no packages; do not assume build or test commands are established.

## Product references and constraints

- `docs/prd.md` defines MVP behavior and security requirements. `plan/initial.md` is a proposed implementation sequence and stack, not an existing architecture.
- The proposed stack is Go `net/http` with HTML templates, SQLite for metadata, and filesystem photo storage; confirm before adding dependencies or treating it as implemented.
- Keep styling functional and minimal; final styling and image optimization are deferred.
- Keep family and admin sessions separate. Enforce access on the server for every photo request so hidden photos cannot be fetched by direct URL.
