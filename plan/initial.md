# Initial Plan — Gallery MVP

## Goal

Build a usable first version of the family gallery with shared-password access, separate admin login, and basic photo management. Focus on functionality and baseline security; defer styling and further optimization.

## Proposed initial stack

- Go with `net/http` and HTML templates; use JavaScript only where interaction requires it.
- SQLite for photo metadata and the filesystem for photo files.
- Keep dependencies to a minimum. Choose the database library and admin credential bootstrap method before implementation.

## Phases

### 1. Application foundation

- Set up the Go application structure, configuration, error pages, and local run instructions.
- Define gallery password configuration and a way to provision initial admin credentials without storing plain-text passwords.
- Set up SQLite and the photo storage location.

**Complete when:** the application runs locally with clear configuration and a working database connection.

### 2. Access and sessions

- Build the family password form and family access session.
- Build the admin login page at `/admin` with a separate admin session.
- Protect admin pages and file endpoints with server-side access checks.

**Complete when:** visitors without a session cannot view photos, and a family session cannot access the admin area.

### 3. Admin photo management

- Add uploads with file type and size validation.
- Store photo metadata, labels, hidden status, and upload time in the database.
- Add actions to delete, hide/show, and update labels.

**Complete when:** all MVP management actions work and database records stay consistent with photo files.

### 4. Family gallery

- Display visible photos and their labels in a simple grid.
- Provide a larger photo view.
- Ensure hidden photos and their files are inaccessible to family members, including through direct URLs.

**Complete when:** family members who pass the password gate can view visible photos but not hidden photos.

### 5. MVP verification

- Test family and admin flows, including failed login, invalid uploads, deletion, and hidden photos.
- Document how to configure and run the application.
- Keep the interface simple and easy to restyle.

## Initial-phase boundaries

- Do not create final visual styling.
- Do not implement image optimization, a CDN, search, albums, or features outside the PRD.
- Prioritize correct flows, access protection, and basic photo operations before expanding the feature set.

## Not included in this documentation task

This document defines the scope and work sequence. Application implementation will begin in a later phase.
