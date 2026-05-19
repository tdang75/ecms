# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ECMS (Enterprise Content Management System) is a fullstack application for managing enterprise documents with taxonomy-based classification, versioning, audit logs, and role-based access control.

- **Backend**: Go 1.22, standard `net/http`, JWT auth, PostgreSQL, MinIO (S3)
- **Frontend**: Vanilla HTML/CSS/JavaScript (no framework), served by Nginx
- **Infrastructure**: Docker Compose orchestrating all services

## Running the Project

```bash
# Start all services
docker compose up

# Build and start
docker compose up --build

# Backend only (requires running Postgres and MinIO separately)
cd backend
go mod tidy
go build -o ecms-server .
./ecms-server
```

**Service ports:**
- Frontend (Nginx): `8080` (HTTP) / `8443` (HTTPS)
- Backend API (Go): `3001`
- PostgreSQL: `5432`
- MinIO S3 API: `9000` / Web console: `9001`
- Gotenberg (Office→PDF): `3000` (internal only, not exposed to host)

**Default credentials (seeded on startup):**
- Admin: `admin` / `admin`

## Architecture

### Backend (`/backend`)

The Go server uses standard `net/http` with a single-file handler pattern. All routes and handlers live in `main.go`. Key design choices:

- **JWT middleware**: Extracts claims and injects into request context; handlers retrieve auth info via context
- **Permission model**: Two-tier — document-level ACL first, then fallback to group system permissions
- **Soft delete**: Documents have a `status` field; hard delete is a separate operation
- **S3 integration**: AWS SigV4-compatible signing implemented manually (no AWS SDK)
- **Office preview**: `handlePreview` fetches the file from S3 and POSTs it to Gotenberg (`/forms/libreoffice/convert`), streaming the resulting PDF inline. `ensureOfficeExt` appends the correct extension (e.g. `.docx`) based on MIME type before sending to Gotenberg, since Gotenberg requires a recognized extension to select the right converter.

### Database Schema (PostgreSQL)

Core tables and their relationships:
- `users` → `user_groups` ← `groups` (membership)
- `groups` → group-level permissions (system-wide)
- `document_classes` (hierarchical taxonomy) → `class_property_assignments` ← `property_templates`
- `documents` → `document_property_values` (polymorphic metadata storage)
- `documents` → `document_acl` (per-principal permissions: read/create/update/delete/download)
- `documents` → `document_versions` (version history with S3 keys)
- `document_annotations` (per-page annotations with normalized 0–1 coordinates)
- `audit_log` (append-only operation trail)

Default seeded data: 6 document classes (Document, Contract, Invoice, Engineering Drawing, HR Document, Report), 14 property templates, 2 groups (administrators/users).

### Frontend (`/frontend`)

Single-page application. All UI logic is in `frontend/index.html` with no build step. Key sections: login, document list/detail, document viewer, taxonomy management, user/group management, ACL editor, stats dashboard.

**Document viewer** (`viewDocument` / `viewer` global object):
- PDF: rendered with PDF.js (CDN) onto a `<canvas>`; supports zoom and page navigation
- TIFF: decoded with inlined UTIF.js; multi-page via IFDs; zoom via CSS scaling
- Office (docx, xlsx, pptx, odt, etc.): converted to PDF server-side via `GET /documents/{id}/preview`, then rendered with PDF.js
- Images: rendered in an `<img>` tag
- Text/code: rendered in a `<pre>` block
- `getViewMode(name, mime)` determines the mode — checks file extension first, then MIME type as fallback (important for files stored without extensions)

**Annotations** (canvas overlay on PDF/TIFF/Office):
- Tool types: `highlight`, `rectangle`, `text`, `redaction`
- Coordinates stored normalized (0–1 fractions of canvas dimensions) so they survive zoom changes
- Persisted to `document_annotations` table via REST API

**Row interactions**:
- Single-click (220 ms debounce): opens detail modal
- Double-click: opens document viewer
- Right-click: context menu with Properties, View, Download, Delete (permission-aware)

**Nav visibility**: Taxonomy and Users & Groups nav items are hidden for regular users; shown only when the logged-in user has `taxonomy:create` or `users:manage` permissions respectively.

### Router

All routes are registered in `(a *App) buildMux() http.Handler` in `main.go`. This function is called by both `main()` and the test suite's `httptest.NewServer`.

### API Surface

```
POST   /auth/login
GET    /auth/me
GET    /documents              # filters: status, category, class_id, search, tags
GET    /documents/{id}
POST   /documents              # multipart upload
PUT    /documents/{id}
PUT    /documents/{id}/properties
POST   /documents/{id}/version
GET    /documents/{id}/download
GET    /documents/{id}/preview        # Office → PDF via Gotenberg
DELETE /documents/{id}               # ?hard=true for permanent deletion
GET    /documents/{id}/acl
PUT    /documents/{id}/acl
DELETE /documents/{id}/acl/{principal}
POST   /documents/{id}/acl/reset
GET    /documents/{id}/annotations
POST   /documents/{id}/annotations
DELETE /documents/{id}/annotations/{annId}
GET/POST/PUT/DELETE /classes
GET    /classes/{id}
POST   /classes/{id}/properties
DELETE /classes/{id}/properties/{propId}
GET/POST         /property-templates
GET/POST/PUT/DELETE /users
GET/PUT          /groups/{id}
GET    /health
GET    /stats
```

## Test Suite

**70 tests** in `backend/*_test.go`. Run with:

```bash
# Requires docker compose postgres on localhost:5432
make test

# Unit tests only (no database needed)
make test-unit

# Override the postgres DSN
TEST_PG_DSN=postgres://user:pass@host:5432/postgres make test
```

### Test files

| File | Coverage |
|---|---|
| `testhelper_test.go` | `TestMain`: creates `ecms_test` DB, wires in-process S3 mock, starts `httptest.Server`; helpers `apiDo`, `mustStatus`, `uploadDoc` |
| `unit_test.go` | `ensureOfficeExt`, `hasPerm`, `getEnv`, `writeJSON/Error`, JWT round-trip & wrong-secret |
| `auth_test.go` | Login success/failure, `/auth/me`, `/health` |
| `documents_test.go` | Upload, list, get, update, soft/hard delete, download, search, versioning, stats |
| `taxonomy_test.go` | Document classes and property templates CRUD, property assignment |
| `users_test.go` | User CRUD, duplicate protection, admin guard, regular-user permission enforcement |
| `annotations_test.go` | Annotation CRUD, all 4 types, invalid-type rejection, ACL set/delete/reset |

### Test infrastructure design

- **Database**: `TestMain` connects to the admin postgres, drops and recreates `ecms_test`, then runs `initDB()` + `seedDefaults()` for a clean slate on every test run.
- **S3 mock**: An in-process `httptest.Server` stores objects in a `map[string][]byte`. No MinIO required during tests.
- **No testcontainers**: Tests connect to the already-running docker-compose postgres (port 5432). If postgres is unreachable they skip gracefully rather than fail.
