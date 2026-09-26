# PRD — Family Photo Gallery

## Purpose

Provide a simple web gallery where family members can view event photos through a password-protected application. An admin can manage photos from a separate page.

## Users

- **Family members:** view photos after entering one shared password.
- **Admin:** sign in with a separate admin username and password, then manage photos.

## MVP scope

### Family gallery

- Visitors without an active access session see a popup-style password form.
- A correct password grants access to the gallery; access is retained in a session.
- The gallery displays photos that are not hidden, along with their labels.
- Visitors can open a photo to view it at a larger size.

### Admin dashboard

- The admin login page is available at `/admin` and is separate from family access.
- Admins can upload and delete photos, hide or show photos, and manage photo labels.
- Hidden photos do not appear in the gallery and cannot be accessed by family members who know the file URL.
- Deletion requires confirmation.

## Technical and security requirements

- Use Go for the backend.
- Initial storage proposal: SQLite for metadata and the filesystem for photo files.
- Never store passwords as plain text; use password hashing.
- Family and admin sessions must be separate. Session cookies must use appropriate security settings, including `HttpOnly` and `SameSite`.
- Enforce all access checks on the server, including when serving photo files.
- Validate uploads by file type and size; never use a user-provided filename or path directly as a storage path.
- Use HTML with minimal functional styling. Keep the interface structure flexible so it can be restyled later.

## Out of scope for the MVP

- Final visual styling or a design system.
- Image optimization, batch processing, and CDN strategy.
- Albums/events, search, comments, favorites, public per-photo links, or individual family accounts.
- Multiple-admin management, self-service password reset, and social features.

## Acceptance criteria

1. The gallery cannot be viewed until the correct family password has been entered.
2. Family access does not grant access to the admin dashboard.
3. An admin can sign in at `/admin` and perform all MVP photo-management actions.
4. Hidden photos are not visible or retrievable by family members, including through a direct URL.
5. Deleted photos no longer appear, and their files are removed from storage.
6. The interface works with minimal styling and does not depend on a final visual design.
