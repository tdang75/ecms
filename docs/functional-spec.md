# ECMS Functional Specification

**Version:** 1.4  
**Date:** 2026-05-20  
**Status:** Current

---

## 1. Overview

ECMS (Enterprise Content Management System) is a web-based application for managing enterprise documents. It provides secure upload, storage, retrieval, versioning, and classification of documents with role-based access control, per-document ACLs, full audit logging, and an in-browser document viewer.

---

## 2. Users and Roles

### 2.1 Groups

The system ships with two built-in groups. Administrators can create additional groups with custom permission sets.

| Group | Default Permissions |
|---|---|
| **administrators** | All permissions |
| **users** | `documents:create`, `documents:read`, `documents:download`, `taxonomy:read` |

### 2.2 Permissions

| Permission | Description |
|---|---|
| `documents:create` | Upload new documents |
| `documents:read` | View document list and metadata |
| `documents:update` | Edit document metadata and upload new versions |
| `documents:delete` | Soft-delete or hard-delete documents |
| `documents:download` | Download file content and view in browser |
| `taxonomy:create` | Create document classes and property templates |
| `taxonomy:read` | View document classes and property templates |
| `taxonomy:update` | Edit document classes and property templates |
| `taxonomy:delete` | Delete document classes and property templates |
| `users:manage` | Create, edit, and delete users and groups |

### 2.3 Navigation Visibility

- **Taxonomy** and **Users & Groups** sections are hidden from users who lack `taxonomy:create` and `users:manage` respectively.
- All other navigation items are visible to authenticated users.

### 2.4 Sidebar Layout

The left sidebar contains, in order:

1. **File Store** — folder tree for navigating the document hierarchy.
2. **Search** — advanced document search view.
3. **Taxonomy** — document class and property template management (hidden for regular users).
4. **Analytics** — usage statistics dashboard.
5. **Users & Groups** — user and group management (hidden for regular users).

---

## 3. Authentication

- Session-based using JWT (HS256, 8-hour expiry).
- Login requires username and password.
- All API endpoints except `POST /auth/login` and `GET /health` require a valid Bearer token.
- Inactive accounts cannot log in.

---

## 4. Document Management

### 4.1 Upload

- Accepts any file type via multipart form upload.
- Stored in S3-compatible object storage (MinIO in local deployments).
- Metadata stored in PostgreSQL.
- On upload: a version 1 record is created, default ACL is applied, and an audit entry is written.
- Optional fields: name (defaults to filename), description, category, class, tags, custom properties.

### 4.2 Document List

- Displays all active documents sorted by creation date (newest first).
- Filter by: status, category, document class (toolbar dropdowns).
- Left sidebar **File Store** folder tree filters documents to a selected folder.
- Document count badge shown per class.

### 4.2.1 Advanced Search

A dedicated **Search** sidebar entry provides a structured search form:

- **Class selector**: optionally restrict results to a specific document class.
- **Full-text field**: searches document name and description.
- **Condition builder**: one or more property conditions, each specifying:
  - Property template (filtered to the selected class)
  - Operator (equals, contains, starts with, greater than, less than, between, etc.)
  - Value(s)
- **Logic**: conditions can be combined with AND or OR.
- Results are returned via `POST /documents/search`.

### 4.3 Document Detail

Clicking a document row opens a detail panel showing:
- Name, description, class, category, tags
- File size, MIME type, version number, created/updated timestamps, created by
- Custom property values
- Version history
- Audit log (last 20 entries)
- ACL entries (administrators and users with update permission only)

### 4.4 Row Interactions

| Interaction | Action |
|---|---|
| Single-click | Opens detail panel (220 ms debounce) |
| Double-click | Opens document viewer |
| Right-click | Context menu: Properties, View, Download, Delete (Delete visible only to users with `documents:delete` permission or document owners) |

### 4.5 Update

- Editable fields: name, description, category, tags, status, custom properties.
- Status values: `active`, `archived`, `deleted`.

#### 4.5.1 Change Document Class

- A document can be reclassified to a different document class via the detail panel.
- The **Change Class** dialog presents a two-panel side-by-side view:
  - **Left panel** (read-only): current class properties and their values.
  - **Right panel** (editable): new class properties pre-filled where a property template ID matches between old and new class; matched fields are highlighted and marked "copied".
- On confirm: the document's `class_id` is updated and all property values are replaced with the new panel's values (`PUT /documents/{id}/properties?replace_all=true`).

### 4.6 Deletion

- **Soft delete**: sets `status = 'deleted'`; document is removed from the active list but not from storage.
- **Hard delete** (`?hard=true`): removes the database record and deletes the S3 object.
- Users with the `documents:delete` system permission **or** the `owner` ACL entry on a document may delete it.

### 4.7 Versioning

- Any user with update permission can upload a new version of a document.
- Each version stores its own S3 key, file size, upload timestamp, and optional comment.
- The document record always points to the latest version.
- Full version history is accessible in the detail panel.

---

## 5. Document Viewer

The in-browser viewer opens as a full-screen overlay. It supports the following file types:

| Mode | Formats | Renderer |
|---|---|---|
| PDF | `.pdf`, `application/pdf` | PDF.js (canvas-based) |
| TIFF | `.tif`, `.tiff`, `image/tiff` | UTIF.js (inline decoder) |
| Office | `.docx`, `.xlsx`, `.pptx`, `.doc`, `.xls`, `.ppt`, `.odt`, `.ods`, `.odp`, and matching MIME types | Converted to PDF server-side via Gotenberg, then rendered with PDF.js |
| Image | `.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`, `.bmp`, `.svg` | `<img>` tag |
| Text / Code | `.txt`, `.md`, `.json`, `.xml`, `.csv`, `.js`, `.ts`, `.go`, `.py`, `.sh`, `.html`, `.css`, `.sql`, and `text/*` MIME | `<pre>` block |

Files without a recognized extension fall back to MIME type detection.

### 5.1 Viewer Controls

- **Zoom**: in/out buttons and percentage display (PDF and TIFF).
- **Page navigation**: previous/next buttons and direct page number input (PDF, TIFF, and Office).
- **Download**: downloads the original file.
- **Annotation sidebar toggle**: shows/hides the annotation panel.

### 5.2 Annotations

Annotations are overlaid on PDF, TIFF, and Office documents via a transparent canvas layer.

| Tool | Description |
|---|---|
| Select | Pan / select existing annotations |
| Highlight | Yellow semi-transparent rectangle |
| Rectangle | Blue semi-transparent rectangle |
| Arrow | Orange directional arrow drawn by dragging from tail to head |
| Text | Green semi-transparent rectangle with label |
| Redaction | Solid black rectangle (obscures content) |
| Approved stamp | Click-to-place green rubber-stamp overlay reading **APPROVED** |
| Rejected stamp | Click-to-place red rubber-stamp overlay reading **REJECTED** |

- **Stamps** are placed with a single click at a fixed size.
- **Arrows** are drawn by dragging; stored as a start point plus a signed delta so direction is preserved regardless of angle (horizontal, vertical, and diagonal arrows all work correctly).
- All other tools require a drag to define the region.
- Stamp annotations are rendered as a rotated rounded-rectangle border with bold uppercase text, mimicking a physical rubber stamp.
- Arrow annotations are rendered as a shaft line with a filled triangular arrowhead.
- Coordinates are stored normalized (0–1 fractions of canvas width/height) so they remain accurate across zoom levels and page sizes.
- Annotations are persisted per document per page in the database.
- Any authenticated user with document read access can create annotations.
- A user can delete their own annotations; users with `documents:update` permission can delete any annotation.

### 5.3 Office Preview

- The backend fetches the file from S3 and POSTs it to Gotenberg (`/forms/libreoffice/convert`).
- If the stored filename has no recognized Office extension, the correct extension is appended based on the document's MIME type before sending to Gotenberg.
- The converted PDF is streamed inline to the browser.
- If Gotenberg is unavailable or conversion fails, the viewer falls back to a download prompt.

---

## 6. Access Control

### 6.1 Document ACL

Each document has a set of ACL entries. Each entry binds a **principal** to a set of **operations**.

- Principal format: `group:<group-name>` or `user:<username>`.
- Operations: `read`, `create`, `update`, `delete`, `download`, `owner`.
- The `owner` operation is special: a principal with `owner` is granted all other operations automatically.
- Default ACL on upload: `group:administrators` gets all operations; `group:users` gets `read` + `download`; the uploading user gets `owner`.

### 6.2 Permission Evaluation

1. Check the document's ACL for the requesting user's principals (own username + all group memberships).
2. If a matching ACL entry grants the operation, or grants `owner` → allow.
3. If no ACL rows exist for the document at all → fall back to group system permissions.
4. Otherwise → deny.

### 6.3 ACL Management

- View ACL: requires `documents:read` + document-level update permission.
- Set/delete/reset ACL: requires document-level update permission.
- Reset restores the default ACL (administrators: all, users: read + download, original uploader: owner).

---

## 7. Taxonomy

### 7.1 Document Classes

- Hierarchical classification system (parent/child classes).
- Each class has a name, optional description, icon (emoji), and optional parent.
- 6 classes seeded by default: Document, Contract, Invoice, Engineering Drawing, HR Document, Report.

### 7.2 Property Templates

- Reusable metadata fields that can be assigned to document classes.
- Data types: `string`, `integer`, `decimal`, `boolean`, `date`, `datetime`, `user`.
- Optional: required flag, multivalued flag, default value, allowed choices list.
- 14 property templates seeded by default (e.g., author, effective_date, invoice_number).

### 7.3 Class–Property Assignments

- A property template can be assigned to one or more classes with a custom sort order and optional override of required/default.
- Assigned properties appear in the document upload and edit forms for documents of that class.

---

## 8. User and Group Management

Accessible only to users with `users:manage` permission.

### 8.1 Users

- Create users with username, email, full name, password, and initial group assignments.
- Edit email, full name, password, active status, and group memberships.
- Delete users (the built-in `admin` account cannot be deleted).
- New users with no explicit group assignment are placed in the `users` group.

### 8.2 Groups

- View all groups and their permission sets.
- Edit a group's description and permissions.
- Built-in groups: `administrators`, `users`.

---

## 9. Audit Log

- Append-only log of all significant operations on documents.
- Recorded actions: `upload`, `update`, `update_properties`, `new_version`, `download`, `soft_delete`, `hard_delete`, `acl_update`.
- Each entry records: document ID, action, actor (username), details (JSON), timestamp.
- Last 20 entries shown in the document detail panel.

---

## 10. Infrastructure

| Component | Technology | Purpose |
|---|---|---|
| Frontend | Nginx + vanilla HTML/CSS/JS | SPA served over HTTPS |
| Backend | Go 1.22, `net/http` | REST API |
| Database | PostgreSQL 16 | Metadata, ACL, annotations, audit log |
| Object storage | MinIO (S3-compatible) | Document file storage |
| Office converter | Gotenberg 8 (LibreOffice) | Office → PDF conversion for viewer |

### 10.1 Service Ports

| Service | Port |
|---|---|
| Frontend (HTTP) | 8080 |
| Frontend (HTTPS) | 8443 |
| Backend API | 3001 (internal) |
| PostgreSQL | 5432 |
| MinIO S3 API | 9000 |
| MinIO web console | 9001 |
| Gotenberg | 3000 (internal) |

### 10.2 Default Credentials

| Account | Username | Password |
|---|---|---|
| Admin user | `admin` | `admin` |
| MinIO | `minioadmin` | `minioadmin` |
| PostgreSQL | `postgres` | `localpassword` |

---

## 11. Sample Clients

The `samples/` directory contains standalone example programs that demonstrate ECMS API usage.

### 11.1 Go API Client (`samples/go-api-client`)

A standalone Go program (no external dependencies beyond the standard library) that:

1. Downloads a sample PDF from one of several public URLs (IRS W-9, W3C sample, Mozilla PDF.js test), tried in randomised order; falls back to generating a minimal valid PDF in-memory if all URLs fail.
2. Authenticates as `admin` against the local ECMS instance (`https://localhost:8443/api`).
3. Uploads the document as a **Contract** with hardcoded metadata (name, description, category, tags, and all four Contract property values).

Run: `cd samples/go-api-client && go run main.go` (requires ECMS running via `docker compose up`).

---

## 12. Test Suite

The backend has 70 automated tests covering:

- Unit tests: `ensureOfficeExt`, `hasPerm`, `getEnv`, JWT, `writeJSON/Error`
- Integration tests: all API endpoints via `httptest.NewServer` with a real PostgreSQL test database (`ecms_test`) and an in-process S3 mock
- `TestAnnotations_AllTypes` covers all seven annotation types including `stamp-approved`, `stamp-rejected`, and `arrow`

Run: `cd backend && go test ./... -timeout 120s` (requires docker-compose postgres on `localhost:5432`).
