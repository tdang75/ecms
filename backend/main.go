package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// ── Config ─────────────────────────────────────────────────────────────────────

type Config struct {
	Port       string
	PGHost     string
	PGPort     string
	PGDatabase string
	PGUser     string
	PGPassword string
	AWSRegion  string
	AWSKeyID   string
	AWSSecret  string
	S3Bucket   string
	S3Endpoint string
	JWTSecret    string
	GotenbergURL string
}

func loadConfig() Config {
	return Config{
		Port:       getEnv("PORT", "3001"),
		PGHost:     getEnv("PG_HOST", "localhost"),
		PGPort:     getEnv("PG_PORT", "5432"),
		PGDatabase: getEnv("PG_DATABASE", "ecms"),
		PGUser:     getEnv("PG_USER", "postgres"),
		PGPassword: getEnv("PG_PASSWORD", ""),
		AWSRegion:  getEnv("AWS_REGION", "us-east-1"),
		AWSKeyID:   getEnv("AWS_ACCESS_KEY_ID", "minioadmin"),
		AWSSecret:  getEnv("AWS_SECRET_ACCESS_KEY", "minioadmin"),
		S3Bucket:   getEnv("S3_BUCKET_NAME", "ecms-documents"),
		S3Endpoint: getEnv("AWS_ENDPOINT_URL", ""),
		JWTSecret:    getEnv("JWT_SECRET", "ecms-change-me-in-production"),
		GotenbergURL: getEnv("GOTENBERG_URL", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ── Permission constants ───────────────────────────────────────────────────────

// Document-level ACL operations
const (
	ACLRead     = "read"
	ACLCreate   = "create"
	ACLUpdate   = "update"
	ACLDelete   = "delete"
	ACLDownload = "download"
	ACLOwner    = "owner" // supersedes all other operations
)

const (
	PermDocCreate   = "documents:create"
	PermDocRead     = "documents:read"
	PermDocUpdate   = "documents:update"
	PermDocDelete   = "documents:delete"
	PermDocDownload = "documents:download"
	PermTaxCreate   = "taxonomy:create"
	PermTaxRead     = "taxonomy:read"
	PermTaxUpdate   = "taxonomy:update"
	PermTaxDelete   = "taxonomy:delete"
	PermUserManage    = "users:manage"
	PermFolderManage  = "folders:manage"
)

var AllPermissions = []string{
	PermDocCreate, PermDocRead, PermDocUpdate, PermDocDelete, PermDocDownload,
	PermTaxCreate, PermTaxRead, PermTaxUpdate, PermTaxDelete,
	PermUserManage, PermFolderManage,
}

const (
	FolderRootID    = "f0000000-0000-0000-0000-000000000001"
	FolderUnfiledID = "f0000000-0000-0000-0000-000000000002"
)

var UserPermissions = []string{
	PermDocCreate, PermDocRead, PermDocDownload,
	PermTaxRead,
}

// ── Models ─────────────────────────────────────────────────────────────────────

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	FullName     string    `json:"full_name"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Group struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"created_at"`
}

type Claims struct {
	UserID      string   `json:"user_id"`
	Username    string   `json:"username"`
	Groups      []string `json:"groups"`
	Permissions []string `json:"permissions"`
	jwt.RegisteredClaims
}

type DocumentClass struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	ParentID    *string   `json:"parent_id"`
	Icon        string    `json:"icon"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PropertyTemplate struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	DataType      string   `json:"data_type"`
	IsRequired    bool     `json:"is_required"`
	IsMultivalued bool     `json:"is_multivalued"`
	DefaultValue  *string  `json:"default_value"`
	Choices       []string `json:"choices"`
	Description   string   `json:"description"`
}

type ClassPropertyAssignment struct {
	ID                 string            `json:"id"`
	ClassID            string            `json:"class_id"`
	PropertyTemplateID string            `json:"property_template_id"`
	IsRequired         *bool             `json:"is_required"`
	DefaultValue       *string           `json:"default_value"`
	SortOrder          int               `json:"sort_order"`
	Template           *PropertyTemplate `json:"template,omitempty"`
}

type PropInput struct {
	PropertyTemplateID string `json:"property_template_id"`
	Value              string `json:"value"`
}

type PropertyValue struct {
	PropertyTemplateID string      `json:"property_template_id"`
	Name               string      `json:"name"`
	DisplayName        string      `json:"display_name"`
	DataType           string      `json:"data_type"`
	Value              interface{} `json:"value"`
}

type Document struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ClassID     *string         `json:"class_id"`
	ClassName   *string         `json:"class_name"`
	ClassIcon   *string         `json:"class_icon"`
	Category    string          `json:"category"`
	Tags        []string        `json:"tags"`
	S3Key       string          `json:"s3_key"`
	S3Bucket    string          `json:"s3_bucket"`
	FileSize    int64           `json:"file_size"`
	MimeType    string          `json:"mime_type"`
	Version     int             `json:"version"`
	Status      string          `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	CreatedBy   string          `json:"created_by"`
	UpdatedBy   string          `json:"updated_by"`
	Properties  []PropertyValue `json:"properties,omitempty"`
}

type ACLEntry struct {
	ID         string   `json:"id"`
	DocumentID string   `json:"document_id"`
	Principal  string   `json:"principal"`  // group name or "user:<username>"
	Operations []string `json:"operations"` // ["read","create","update","delete","download"]
}

type DocVersion struct {
	ID         string    `json:"id"`
	DocumentID string    `json:"document_id"`
	Version    int       `json:"version"`
	S3Key      string    `json:"s3_key"`
	FileSize   int64     `json:"file_size"`
	UploadedAt time.Time `json:"uploaded_at"`
	Comment    string    `json:"comment"`
}

type AuditEntry struct {
	ID         int             `json:"id"`
	DocumentID string          `json:"document_id"`
	Action     string          `json:"action"`
	Actor      string          `json:"actor"`
	Details    json.RawMessage `json:"details"`
	CreatedAt  time.Time       `json:"created_at"`
}

type SearchCondition struct {
	PropertyID string `json:"property_id"`
	Operator   string `json:"operator"`
	Value      string `json:"value"`
	Value2     string `json:"value2"`
}

type AdvancedSearchRequest struct {
	Status     string            `json:"status"`
	Text       string            `json:"text"`
	ClassID    string            `json:"class_id"`
	FolderID   string            `json:"folder_id"`
	Logic      string            `json:"logic"`
	Conditions []SearchCondition `json:"conditions"`
}

type Annotation struct {
	ID         string    `json:"id"`
	DocumentID string    `json:"document_id"`
	Version    int       `json:"version"`
	Page       int       `json:"page"`
	Type       string    `json:"type"`
	X          float64   `json:"x"`
	Y          float64   `json:"y"`
	Width      float64   `json:"width"`
	Height     float64   `json:"height"`
	Color      string    `json:"color,omitempty"`
	Content    string    `json:"content,omitempty"`
	Author     string    `json:"author"`
	CreatedAt  time.Time `json:"created_at"`
}

type Folder struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ParentID  *string   `json:"parent_id"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
	Children  []*Folder `json:"children,omitempty"`
}

// ── App ────────────────────────────────────────────────────────────────────────

type App struct {
	cfg Config
	db  *pgxpool.Pool
}

// ── JWT ────────────────────────────────────────────────────────────────────────

func (a *App) generateToken(userID, username string, groups, permissions []string) (string, error) {
	claims := Claims{
		UserID:      userID,
		Username:    username,
		Groups:      groups,
		Permissions: permissions,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "ecms",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(a.cfg.JWTSecret))
}

func (a *App) parseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(a.cfg.JWTSecret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

func (a *App) claimsFromRequest(r *http.Request) *Claims {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return nil
	}
	claims, err := a.parseToken(strings.TrimPrefix(auth, "Bearer "))
	if err != nil {
		return nil
	}
	return claims
}

func hasPerm(claims *Claims, perm string) bool {
	if claims == nil {
		return false
	}
	for _, p := range claims.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// ── Auth Middleware ────────────────────────────────────────────────────────────

type key int

const claimsKey key = 0

func (a *App) requireAuth(perm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := a.claimsFromRequest(r)
		if claims == nil {
			writeError(w, 401, "authentication required")
			return
		}
		if perm != "" && !hasPerm(claims, perm) {
			writeError(w, 403, fmt.Sprintf("permission denied: %s required", perm))
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next(w, r.WithContext(ctx))
	}
}

// requireAuthOrOwner is like requireAuth but also passes the system-permission
// check if the user holds the "owner" ACL operation on the target document
// (identified by the {id} path value).
func (a *App) requireAuthOrOwner(perm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := a.claimsFromRequest(r)
		if claims == nil {
			writeError(w, 401, "authentication required")
			return
		}
		if perm != "" && !hasPerm(claims, perm) {
			docID := r.PathValue("id")
			if docID == "" || !a.isDocOwner(r.Context(), docID, claims) {
				writeError(w, 403, fmt.Sprintf("permission denied: %s required", perm))
				return
			}
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next(w, r.WithContext(ctx))
	}
}

// isDocOwner returns true if the user holds the "owner" ACL operation on docID.
func (a *App) isDocOwner(ctx context.Context, docID string, claims *Claims) bool {
	if claims == nil {
		return false
	}
	var count int
	a.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM document_acl
		WHERE document_id=$1 AND principal=$2 AND 'owner' = ANY(operations)`,
		docID, "user:"+claims.Username).Scan(&count)
	return count > 0
}

func claimsFrom(r *http.Request) *Claims {
	c, _ := r.Context().Value(claimsKey).(*Claims)
	return c
}

// ── S3 / SigV4 ─────────────────────────────────────────────────────────────────

func (a *App) s3URL(key string) string {
	if a.cfg.S3Endpoint != "" {
		return fmt.Sprintf("%s/%s/%s", strings.TrimRight(a.cfg.S3Endpoint, "/"), a.cfg.S3Bucket, key)
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", a.cfg.S3Bucket, a.cfg.AWSRegion, key)
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (a *App) signRequest(req *http.Request, body []byte) {
	region, service := a.cfg.AWSRegion, "s3"
	now := time.Now().UTC()
	date, datetime := now.Format("20060102"), now.Format("20060102T150405Z")
	bodyHash := sha256Hex(body)
	req.Header.Set("x-amz-date", datetime)
	req.Header.Set("x-amz-content-sha256", bodyHash)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n", req.Host, bodyHash, datetime)
	canonicalRequest := strings.Join([]string{req.Method, req.URL.EscapedPath(), req.URL.RawQuery, canonicalHeaders, signedHeaders, bodyHash}, "\n")
	credScope := fmt.Sprintf("%s/%s/%s/aws4_request", date, region, service)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s", datetime, credScope, sha256Hex([]byte(canonicalRequest)))
	signingKey := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256([]byte("AWS4"+a.cfg.AWSSecret), date), region), service), "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", a.cfg.AWSKeyID, credScope, signedHeaders, sig))
}

func (a *App) s3Put(key, contentType string, body []byte) error {
	req, _ := http.NewRequest(http.MethodPut, a.s3URL(key), bytes.NewReader(body))
	req.Host = req.URL.Host
	req.Header.Set("Content-Type", contentType)
	a.signRequest(req, body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3 put %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (a *App) s3Get(key string) (*http.Response, error) {
	req, _ := http.NewRequest(http.MethodGet, a.s3URL(key), nil)
	req.Host = req.URL.Host
	a.signRequest(req, []byte{})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("s3 get %d: %s", resp.StatusCode, b)
	}
	return resp, nil
}

func (a *App) s3Delete(key string) {
	req, _ := http.NewRequest(http.MethodDelete, a.s3URL(key), nil)
	req.Host = req.URL.Host
	a.signRequest(req, []byte{})
	resp, _ := http.DefaultClient.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
}

func (a *App) createBucket() error {
	bucketURL := fmt.Sprintf("%s/%s", strings.TrimRight(a.cfg.S3Endpoint, "/"), a.cfg.S3Bucket)
	if a.cfg.S3Endpoint == "" {
		bucketURL = fmt.Sprintf("https://s3.%s.amazonaws.com/%s", a.cfg.AWSRegion, a.cfg.S3Bucket)
	}
	headReq, _ := http.NewRequest(http.MethodHead, bucketURL, nil)
	headReq.Host = headReq.URL.Host
	a.signRequest(headReq, []byte{})
	if hr, err := http.DefaultClient.Do(headReq); err == nil {
		hr.Body.Close()
		if hr.StatusCode == http.StatusOK {
			log.Printf("✅ S3 bucket '%s' exists", a.cfg.S3Bucket)
			return nil
		}
	}
	req, _ := http.NewRequest(http.MethodPut, bucketURL, nil)
	req.Host = req.URL.Host
	a.signRequest(req, []byte{})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create bucket %d: %s", resp.StatusCode, b)
	}
	log.Printf("✅ S3 bucket '%s' ready", a.cfg.S3Bucket)
	return nil
}

// ── DB Init ────────────────────────────────────────────────────────────────────

func (a *App) initDB() error {
	schema := `
	-- Auth tables
	CREATE TABLE IF NOT EXISTS users (
		id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		username      TEXT NOT NULL UNIQUE,
		email         TEXT NOT NULL UNIQUE,
		full_name     TEXT DEFAULT '',
		password_hash TEXT NOT NULL,
		is_active     BOOLEAN DEFAULT TRUE,
		created_at    TIMESTAMPTZ DEFAULT NOW(),
		updated_at    TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE TABLE IF NOT EXISTS groups (
		id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name        TEXT NOT NULL UNIQUE,
		description TEXT DEFAULT '',
		permissions TEXT[] DEFAULT '{}',
		created_at  TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE TABLE IF NOT EXISTS user_groups (
		user_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
		PRIMARY KEY (user_id, group_id)
	);

	-- Taxonomy tables
	CREATE TABLE IF NOT EXISTS document_classes (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name TEXT NOT NULL UNIQUE, description TEXT DEFAULT '',
		parent_id UUID REFERENCES document_classes(id) ON DELETE SET NULL,
		icon TEXT DEFAULT '📄', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE TABLE IF NOT EXISTS property_templates (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name TEXT NOT NULL UNIQUE, display_name TEXT NOT NULL,
		data_type TEXT NOT NULL CHECK (data_type IN ('string','integer','decimal','boolean','date','datetime','user')),
		is_required BOOLEAN DEFAULT FALSE, is_multivalued BOOLEAN DEFAULT FALSE,
		default_value TEXT, choices TEXT[], description TEXT DEFAULT '',
		created_at TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE TABLE IF NOT EXISTS class_property_assignments (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		class_id UUID NOT NULL REFERENCES document_classes(id) ON DELETE CASCADE,
		property_template_id UUID NOT NULL REFERENCES property_templates(id) ON DELETE CASCADE,
		is_required BOOLEAN, default_value TEXT, sort_order INTEGER DEFAULT 0,
		UNIQUE (class_id, property_template_id)
	);

	-- Document tables
	CREATE TABLE IF NOT EXISTS documents (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name TEXT NOT NULL, description TEXT DEFAULT '',
		class_id UUID REFERENCES document_classes(id) ON DELETE SET NULL,
		category TEXT DEFAULT 'General', tags TEXT[] DEFAULT '{}',
		s3_key TEXT NOT NULL, s3_bucket TEXT NOT NULL,
		file_size BIGINT DEFAULT 0, mime_type TEXT DEFAULT '',
		version INTEGER DEFAULT 1,
		status TEXT DEFAULT 'active' CHECK (status IN ('active','archived','deleted')),
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(),
		created_by TEXT DEFAULT 'system'
	);
	CREATE TABLE IF NOT EXISTS document_property_values (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		property_template_id UUID NOT NULL REFERENCES property_templates(id) ON DELETE CASCADE,
		value_string TEXT, value_integer BIGINT, value_decimal NUMERIC(18,6),
		value_boolean BOOLEAN, value_date DATE, value_datetime TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE TABLE IF NOT EXISTS document_acl (
		id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		principal   TEXT NOT NULL,
		operations  TEXT[] DEFAULT '{}',
		UNIQUE (document_id, principal)
	);
	CREATE INDEX IF NOT EXISTS idx_acl_document ON document_acl(document_id);
	CREATE TABLE IF NOT EXISTS document_versions (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		document_id UUID REFERENCES documents(id) ON DELETE CASCADE,
		version INTEGER NOT NULL, s3_key TEXT NOT NULL,
		file_size BIGINT DEFAULT 0, uploaded_at TIMESTAMPTZ DEFAULT NOW(),
		comment TEXT DEFAULT '', UNIQUE (document_id, version)
	);
	CREATE TABLE IF NOT EXISTS audit_log (
		id SERIAL PRIMARY KEY, document_id UUID, action TEXT NOT NULL,
		actor TEXT DEFAULT 'system', details JSONB, created_at TIMESTAMPTZ DEFAULT NOW()
	);

	-- Indexes
	CREATE INDEX IF NOT EXISTS idx_documents_status     ON documents(status);
	CREATE INDEX IF NOT EXISTS idx_documents_class      ON documents(class_id);
	CREATE INDEX IF NOT EXISTS idx_documents_tags       ON documents USING GIN(tags);
	CREATE INDEX IF NOT EXISTS idx_prop_values_document ON document_property_values(document_id);
	CREATE INDEX IF NOT EXISTS idx_audit_document       ON audit_log(document_id);
	CREATE INDEX IF NOT EXISTS idx_user_groups_user     ON user_groups(user_id);

	-- Annotations
	CREATE TABLE IF NOT EXISTS document_annotations (
		id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		page        INTEGER NOT NULL DEFAULT 1,
		annotation_type TEXT NOT NULL CHECK (annotation_type IN ('highlight','rectangle','text','redaction','stamp-approved','stamp-rejected','arrow')),
		x           DOUBLE PRECISION NOT NULL DEFAULT 0,
		y           DOUBLE PRECISION NOT NULL DEFAULT 0,
		width       DOUBLE PRECISION NOT NULL DEFAULT 0,
		height      DOUBLE PRECISION NOT NULL DEFAULT 0,
		color       TEXT,
		content     TEXT,
		author      TEXT NOT NULL DEFAULT '',
		created_at  TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_annotations_document ON document_annotations(document_id);

	-- Folder tables
	CREATE TABLE IF NOT EXISTS folders (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		parent_id  TEXT REFERENCES folders(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		created_by TEXT NOT NULL DEFAULT 'system'
	);
	CREATE TABLE IF NOT EXISTS document_folders (
		document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		folder_id   TEXT NOT NULL REFERENCES folders(id)   ON DELETE CASCADE,
		filed_at    TIMESTAMPTZ DEFAULT NOW(),
		filed_by    TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (document_id, folder_id)
	);
	CREATE INDEX IF NOT EXISTS idx_doc_folders_doc    ON document_folders(document_id);
	CREATE INDEX IF NOT EXISTS idx_doc_folders_folder ON document_folders(folder_id);

	-- Triggers
	CREATE OR REPLACE FUNCTION update_updated_at() RETURNS TRIGGER AS $$
	BEGIN NEW.updated_at = NOW(); RETURN NEW; END; $$ LANGUAGE plpgsql;
	DROP TRIGGER IF EXISTS set_updated_at ON documents;
	CREATE TRIGGER set_updated_at BEFORE UPDATE ON documents FOR EACH ROW EXECUTE FUNCTION update_updated_at();
	DROP TRIGGER IF EXISTS set_updated_at ON document_classes;
	CREATE TRIGGER set_updated_at BEFORE UPDATE ON document_classes FOR EACH ROW EXECUTE FUNCTION update_updated_at();
	DROP TRIGGER IF EXISTS set_updated_at ON users;
	CREATE TRIGGER set_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION update_updated_at();
	`
	if _, err := a.db.Exec(context.Background(), schema); err != nil {
		return err
	}
	// Extend annotation type constraint to include stamp types (idempotent migration).
	a.db.Exec(context.Background(), `
		DO $$ BEGIN
			ALTER TABLE document_annotations DROP CONSTRAINT IF EXISTS document_annotations_annotation_type_check;
			ALTER TABLE document_annotations ADD CONSTRAINT document_annotations_annotation_type_check
				CHECK (annotation_type IN ('highlight','rectangle','text','redaction','stamp-approved','stamp-rejected','arrow'));
		EXCEPTION WHEN others THEN NULL;
		END $$;
	`)
	// Add doc_version column to annotations (idempotent migration).
	a.db.Exec(context.Background(), `
		DO $$ BEGIN
			ALTER TABLE document_annotations ADD COLUMN doc_version INTEGER NOT NULL DEFAULT 1;
		EXCEPTION WHEN others THEN NULL;
		END $$;
	`)
	// Add updated_by column to documents (idempotent migration).
	a.db.Exec(context.Background(), `
		DO $$ BEGIN
			ALTER TABLE documents ADD COLUMN updated_by TEXT NOT NULL DEFAULT '';
		EXCEPTION WHEN others THEN NULL;
		END $$;
	`)
	log.Println("✅ Database schema initialized")
	return nil
}

func (a *App) seedDefaults() error {
	ctx := context.Background()

	// Seed groups — insert each separately so pgx binds []string → TEXT[] correctly
	_, err := a.db.Exec(ctx, `
		INSERT INTO groups (id,name,description,permissions) VALUES
		  ('00000000-0000-0000-0001-000000000001','administrators','Full system access',$1)
		ON CONFLICT (name) DO UPDATE SET permissions=EXCLUDED.permissions`,
		AllPermissions)
	if err != nil {
		return fmt.Errorf("seed administrators group: %w", err)
	}
	_, err = a.db.Exec(ctx, `
		INSERT INTO groups (id,name,description,permissions) VALUES
		  ('00000000-0000-0000-0001-000000000002','users','Standard user access',$1)
		ON CONFLICT (name) DO UPDATE SET permissions=EXCLUDED.permissions`,
		UserPermissions)
	if err != nil {
		return fmt.Errorf("seed users group: %w", err)
	}

	// Seed admin user
	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx, `
		INSERT INTO users (id,username,email,full_name,password_hash) VALUES
		  ('00000000-0000-0000-0002-000000000001','admin','admin@ecms.local','Administrator',$1)
		ON CONFLICT (username) DO UPDATE SET password_hash=EXCLUDED.password_hash`, string(hash))
	if err != nil {
		return fmt.Errorf("seed admin user: %w", err)
	}

	// Assign admin to administrators group
	_, err = a.db.Exec(ctx, `
		INSERT INTO user_groups (user_id,group_id) VALUES
		  ('00000000-0000-0000-0002-000000000001','00000000-0000-0000-0001-000000000001')
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return fmt.Errorf("seed admin group: %w", err)
	}

	// Seed document classes
	_, err = a.db.Exec(ctx, `
		INSERT INTO document_classes (id,name,description,icon) VALUES
		  ('00000000-0000-0000-0000-000000000001','Document','Base document class','📄'),
		  ('00000000-0000-0000-0000-000000000002','Contract','Legal contracts and agreements','⚖️'),
		  ('00000000-0000-0000-0000-000000000003','Invoice','Invoices and billing documents','🧾'),
		  ('00000000-0000-0000-0000-000000000004','Engineering Drawing','Technical drawings','📐'),
		  ('00000000-0000-0000-0000-000000000005','HR Document','Human resources documents','👥'),
		  ('00000000-0000-0000-0000-000000000006','Report','Business and technical reports','📊')
		ON CONFLICT (name) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("seed classes: %w", err)
	}

	// Seed property templates
	_, err = a.db.Exec(ctx, `
		INSERT INTO property_templates (id,name,display_name,data_type,is_required,description) VALUES
		  ('10000000-0000-0000-0000-000000000001','author','Author','string',false,'Document author'),
		  ('10000000-0000-0000-0000-000000000002','effective_date','Effective Date','date',false,'Date document takes effect'),
		  ('10000000-0000-0000-0000-000000000003','expiry_date','Expiry Date','date',false,'Date document expires'),
		  ('10000000-0000-0000-0000-000000000004','contract_value','Contract Value','decimal',false,'Monetary value'),
		  ('10000000-0000-0000-0000-000000000005','counterparty','Counterparty','string',false,'Other party in contract'),
		  ('10000000-0000-0000-0000-000000000006','invoice_number','Invoice Number','string',true,'Unique invoice ID'),
		  ('10000000-0000-0000-0000-000000000007','invoice_amount','Invoice Amount','decimal',true,'Total invoice amount'),
		  ('10000000-0000-0000-0000-000000000008','vendor','Vendor','string',false,'Vendor or supplier'),
		  ('10000000-0000-0000-0000-000000000009','drawing_number','Drawing Number','string',true,'Engineering drawing ref'),
		  ('10000000-0000-0000-0000-000000000010','revision','Revision','string',false,'Revision identifier'),
		  ('10000000-0000-0000-0000-000000000011','department','Department','string',false,'Owning department'),
		  ('10000000-0000-0000-0000-000000000012','employee_id','Employee ID','string',false,'Related employee ID'),
		  ('10000000-0000-0000-0000-000000000013','confidential','Confidential','boolean',false,'Is document confidential'),
		  ('10000000-0000-0000-0000-000000000014','report_period','Report Period','string',false,'Period covered by report')
		ON CONFLICT (name) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("seed templates: %w", err)
	}

	// Seed class-property assignments
	_, err = a.db.Exec(ctx, `
		INSERT INTO class_property_assignments (class_id,property_template_id,sort_order) VALUES
		  ('00000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000005',1),
		  ('00000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000004',2),
		  ('00000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000002',3),
		  ('00000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000003',4),
		  ('00000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000013',5),
		  ('00000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000006',1),
		  ('00000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000007',2),
		  ('00000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000008',3),
		  ('00000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000002',4),
		  ('00000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000009',1),
		  ('00000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000010',2),
		  ('00000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000001',3),
		  ('00000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000012',1),
		  ('00000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000011',2),
		  ('00000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000013',3),
		  ('00000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000014',1),
		  ('00000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000011',2),
		  ('00000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000001',3)
		ON CONFLICT (class_id,property_template_id) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("seed class props: %w", err)
	}

	// Seed root folders
	_, err = a.db.Exec(ctx, `
		INSERT INTO folders (id, name, parent_id, created_by) VALUES
		  ($1, 'File Store',     NULL, 'system'),
		  ($2, 'Unfiled Folder', $1,   'system')
		ON CONFLICT (id) DO NOTHING`,
		FolderRootID, FolderUnfiledID)
	if err != nil {
		return fmt.Errorf("seed folders: %w", err)
	}

	log.Printf("✅ Default data seeded — admin password hash set (len=%d)", len(hash))
	return nil
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) audit(docID, action, actor string, details any) {
	b, _ := json.Marshal(details)
	a.db.Exec(context.Background(),
		`INSERT INTO audit_log (document_id,action,actor,details) VALUES ($1,$2,$3,$4)`,
		docID, action, actor, b)
}

const docCols = `d.id,d.name,d.description,d.class_id,dc.name,dc.icon,d.category,d.tags,d.s3_key,d.s3_bucket,d.file_size,d.mime_type,d.version,d.status,d.created_at,d.updated_at,d.created_by,COALESCE(d.updated_by,'')`
const docFrom = `FROM documents d LEFT JOIN document_classes dc ON d.class_id=dc.id`

func scanDoc(row interface{ Scan(...any) error }) (Document, error) {
	var d Document
	err := row.Scan(&d.ID, &d.Name, &d.Description, &d.ClassID, &d.ClassName, &d.ClassIcon,
		&d.Category, &d.Tags, &d.S3Key, &d.S3Bucket, &d.FileSize, &d.MimeType,
		&d.Version, &d.Status, &d.CreatedAt, &d.UpdatedAt, &d.CreatedBy, &d.UpdatedBy)
	if d.Tags == nil {
		d.Tags = []string{}
	}
	return d, err
}

func (a *App) loadProperties(ctx context.Context, docID string) ([]PropertyValue, error) {
	rows, err := a.db.Query(ctx, `
		SELECT pt.id,pt.name,pt.display_name,pt.data_type,
		       pv.value_string,pv.value_integer,pv.value_decimal,
		       pv.value_boolean,pv.value_date,pv.value_datetime
		FROM document_property_values pv
		JOIN property_templates pt ON pv.property_template_id=pt.id
		WHERE pv.document_id=$1 ORDER BY pt.name`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var props []PropertyValue
	for rows.Next() {
		var ptID, ptName, ptDisp, ptType string
		var vs *string; var vi *int64; var vd *float64
		var vb *bool; var vdate, vdt *time.Time
		rows.Scan(&ptID, &ptName, &ptDisp, &ptType, &vs, &vi, &vd, &vb, &vdate, &vdt)
		pv := PropertyValue{PropertyTemplateID: ptID, Name: ptName, DisplayName: ptDisp, DataType: ptType}
		switch ptType {
		case "string", "user": pv.Value = vs
		case "integer":        pv.Value = vi
		case "decimal":        pv.Value = vd
		case "boolean":        pv.Value = vb
		case "date":           if vdate != nil { pv.Value = vdate.Format("2006-01-02") }
		case "datetime":       pv.Value = vdt
		}
		props = append(props, pv)
	}
	return props, nil
}

func (a *App) savePropertyValues(ctx context.Context, docID string, inputs []PropInput) {
	for _, inp := range inputs {
		var dataType string
		if err := a.db.QueryRow(ctx, `SELECT data_type FROM property_templates WHERE id=$1`, inp.PropertyTemplateID).Scan(&dataType); err != nil {
			continue
		}
		pvID := uuid.New().String()
		switch dataType {
		case "string", "user":
			a.db.Exec(ctx, `INSERT INTO document_property_values (id,document_id,property_template_id,value_string) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, pvID, docID, inp.PropertyTemplateID, inp.Value)
		case "integer":
			var vi int64; fmt.Sscanf(inp.Value, "%d", &vi)
			a.db.Exec(ctx, `INSERT INTO document_property_values (id,document_id,property_template_id,value_integer) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, pvID, docID, inp.PropertyTemplateID, vi)
		case "decimal":
			var vd float64; fmt.Sscanf(inp.Value, "%f", &vd)
			a.db.Exec(ctx, `INSERT INTO document_property_values (id,document_id,property_template_id,value_decimal) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, pvID, docID, inp.PropertyTemplateID, vd)
		case "boolean":
			vb := inp.Value == "true" || inp.Value == "1"
			a.db.Exec(ctx, `INSERT INTO document_property_values (id,document_id,property_template_id,value_boolean) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, pvID, docID, inp.PropertyTemplateID, vb)
		case "date":
			a.db.Exec(ctx, `INSERT INTO document_property_values (id,document_id,property_template_id,value_date) VALUES ($1,$2,$3,$4::date) ON CONFLICT DO NOTHING`, pvID, docID, inp.PropertyTemplateID, inp.Value)
		case "datetime":
			a.db.Exec(ctx, `INSERT INTO document_property_values (id,document_id,property_template_id,value_datetime) VALUES ($1,$2,$3,$4::timestamptz) ON CONFLICT DO NOTHING`, pvID, docID, inp.PropertyTemplateID, inp.Value)
		}
	}
}

// ── Auth Handlers ──────────────────────────────────────────────────────────────

// POST /auth/login
func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Username == "" || body.Password == "" {
		writeError(w, 400, "username and password required"); return
	}

	var userID, passwordHash string
	var user User
	err := a.db.QueryRow(context.Background(),
		`SELECT id,username,email,full_name,is_active,password_hash,created_at,updated_at
		 FROM users WHERE username=$1`, body.Username).
		Scan(&userID, &user.Username, &user.Email, &user.FullName, &user.IsActive,
			&passwordHash, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		log.Printf("login: user '%s' not found: %v", body.Username, err)
		writeError(w, 401, "invalid credentials"); return
	}
	if !user.IsActive {
		writeError(w, 403, "account disabled"); return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(body.Password)); err != nil {
		log.Printf("login: wrong password for user '%s': %v", body.Username, err)
		writeError(w, 401, "invalid credentials"); return
	}

	// Load groups and aggregate permissions
	rows, _ := a.db.Query(context.Background(),
		`SELECT g.name, g.permissions FROM groups g
		 JOIN user_groups ug ON g.id=ug.group_id WHERE ug.user_id=$1`, userID)
	defer rows.Close()
	var groups []string
	permSet := map[string]bool{}
	for rows.Next() {
		var gname string; var perms []string
		rows.Scan(&gname, &perms)
		groups = append(groups, gname)
		for _, p := range perms {
			permSet[p] = true
		}
	}
	perms := make([]string, 0, len(permSet))
	for p := range permSet {
		perms = append(perms, p)
	}

	token, err := a.generateToken(userID, user.Username, groups, perms)
	if err != nil {
		writeError(w, 500, "token generation failed"); return
	}

	writeJSON(w, 200, map[string]any{
		"token":       token,
		"user":        map[string]any{"id": userID, "username": user.Username, "email": user.Email, "full_name": user.FullName},
		"groups":      groups,
		"permissions": perms,
		"expires_in":  28800,
	})
}

// GET /auth/me
func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r)
	writeJSON(w, 200, map[string]any{
		"user_id":     claims.UserID,
		"username":    claims.Username,
		"groups":      claims.Groups,
		"permissions": claims.Permissions,
	})
}

// ── User Management Handlers ───────────────────────────────────────────────────

// GET /users
func (a *App) handleListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(context.Background(),
		`SELECT u.id,u.username,u.email,u.full_name,u.is_active,u.created_at,u.updated_at,
		        COALESCE(array_agg(g.name) FILTER (WHERE g.name IS NOT NULL),'{}')
		 FROM users u LEFT JOIN user_groups ug ON u.id=ug.user_id LEFT JOIN groups g ON ug.group_id=g.id
		 GROUP BY u.id ORDER BY u.username`)
	if err != nil {
		writeError(w, 500, err.Error()); return
	}
	defer rows.Close()
	users := []map[string]any{}
	for rows.Next() {
		var u User; var groups []string
		rows.Scan(&u.ID, &u.Username, &u.Email, &u.FullName, &u.IsActive, &u.CreatedAt, &u.UpdatedAt, &groups)
		users = append(users, map[string]any{"id": u.ID, "username": u.Username, "email": u.Email, "full_name": u.FullName, "is_active": u.IsActive, "created_at": u.CreatedAt, "groups": groups})
	}
	writeJSON(w, 200, users)
}

// POST /users
func (a *App) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string   `json:"username"`
		Email    string   `json:"email"`
		FullName string   `json:"full_name"`
		Password string   `json:"password"`
		Groups   []string `json:"groups"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Username == "" || body.Password == "" || body.Email == "" {
		writeError(w, 400, "username, email and password required"); return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, 500, "hash error"); return
	}
	userID := uuid.New().String()
	_, err = a.db.Exec(context.Background(),
		`INSERT INTO users (id,username,email,full_name,password_hash) VALUES ($1,$2,$3,$4,$5)`,
		userID, body.Username, body.Email, body.FullName, string(hash))
	if err != nil {
		writeError(w, 409, "username or email already exists"); return
	}
	// Assign groups
	for _, gname := range body.Groups {
		a.db.Exec(context.Background(),
			`INSERT INTO user_groups (user_id,group_id) SELECT $1,id FROM groups WHERE name=$2 ON CONFLICT DO NOTHING`,
			userID, gname)
	}
	// Default: assign to users group
	if len(body.Groups) == 0 {
		a.db.Exec(context.Background(),
			`INSERT INTO user_groups (user_id,group_id) SELECT $1,id FROM groups WHERE name='users' ON CONFLICT DO NOTHING`,
			userID)
	}
	writeJSON(w, 201, map[string]any{"id": userID, "username": body.Username, "email": body.Email})
}

// PUT /users/{id}
func (a *App) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Email    *string  `json:"email"`
		FullName *string  `json:"full_name"`
		Password *string  `json:"password"`
		IsActive *bool    `json:"is_active"`
		Groups   []string `json:"groups"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	if body.Password != nil {
		hash, _ := bcrypt.GenerateFromPassword([]byte(*body.Password), bcrypt.DefaultCost)
		hashStr := string(hash)
		body.Password = &hashStr
		a.db.Exec(context.Background(), `UPDATE users SET password_hash=$1,updated_at=NOW() WHERE id=$2`, *body.Password, id)
	}
	a.db.Exec(context.Background(),
		`UPDATE users SET email=COALESCE($1,email),full_name=COALESCE($2,full_name),is_active=COALESCE($3,is_active),updated_at=NOW() WHERE id=$4`,
		body.Email, body.FullName, body.IsActive, id)

	if body.Groups != nil {
		a.db.Exec(context.Background(), `DELETE FROM user_groups WHERE user_id=$1`, id)
		for _, gname := range body.Groups {
			a.db.Exec(context.Background(),
				`INSERT INTO user_groups (user_id,group_id) SELECT $1,id FROM groups WHERE name=$2 ON CONFLICT DO NOTHING`,
				id, gname)
		}
	}
	writeJSON(w, 200, map[string]bool{"success": true})
}

// DELETE /users/{id}
func (a *App) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Protect admin
	if id == "00000000-0000-0000-0002-000000000001" {
		writeError(w, 403, "cannot delete the built-in admin user"); return
	}
	a.db.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id)
	writeJSON(w, 200, map[string]bool{"success": true})
}

// GET /groups
func (a *App) handleListGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(context.Background(),
		`SELECT id,name,description,permissions,created_at FROM groups ORDER BY name`)
	if err != nil {
		writeError(w, 500, err.Error()); return
	}
	defer rows.Close()
	groups := []Group{}
	for rows.Next() {
		var g Group
		rows.Scan(&g.ID, &g.Name, &g.Description, &g.Permissions, &g.CreatedAt)
		if g.Permissions == nil { g.Permissions = []string{} }
		groups = append(groups, g)
	}
	writeJSON(w, 200, groups)
}

// PUT /groups/{id}
func (a *App) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Description *string  `json:"description"`
		Permissions []string `json:"permissions"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	a.db.Exec(context.Background(),
		`UPDATE groups SET description=COALESCE($1,description),permissions=COALESCE($2,permissions) WHERE id=$3`,
		body.Description, body.Permissions, id)
	writeJSON(w, 200, map[string]bool{"success": true})
}

// ── Taxonomy Handlers ──────────────────────────────────────────────────────────

func (a *App) handleListClasses(w http.ResponseWriter, r *http.Request) {
	rows, _ := a.db.Query(context.Background(), `SELECT id,name,description,parent_id,icon,created_at,updated_at FROM document_classes ORDER BY name`)
	defer rows.Close()
	classes := []DocumentClass{}
	for rows.Next() {
		var c DocumentClass
		rows.Scan(&c.ID, &c.Name, &c.Description, &c.ParentID, &c.Icon, &c.CreatedAt, &c.UpdatedAt)
		classes = append(classes, c)
	}
	writeJSON(w, 200, classes)
}

func (a *App) handleGetClass(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var c DocumentClass
	if err := a.db.QueryRow(context.Background(),
		`SELECT id,name,description,parent_id,icon,created_at,updated_at FROM document_classes WHERE id=$1`, id).
		Scan(&c.ID, &c.Name, &c.Description, &c.ParentID, &c.Icon, &c.CreatedAt, &c.UpdatedAt); err != nil {
		writeError(w, 404, "class not found"); return
	}
	rows, _ := a.db.Query(context.Background(), `
		SELECT cpa.id,cpa.class_id,cpa.property_template_id,cpa.is_required,cpa.default_value,cpa.sort_order,
		       pt.id,pt.name,pt.display_name,pt.data_type,pt.is_required,pt.is_multivalued,pt.default_value,pt.choices,pt.description
		FROM class_property_assignments cpa
		JOIN property_templates pt ON cpa.property_template_id=pt.id
		WHERE cpa.class_id=$1 ORDER BY cpa.sort_order`, id)
	defer rows.Close()
	assignments := []ClassPropertyAssignment{}
	for rows.Next() {
		var a ClassPropertyAssignment; var pt PropertyTemplate
		rows.Scan(&a.ID, &a.ClassID, &a.PropertyTemplateID, &a.IsRequired, &a.DefaultValue, &a.SortOrder,
			&pt.ID, &pt.Name, &pt.DisplayName, &pt.DataType, &pt.IsRequired, &pt.IsMultivalued, &pt.DefaultValue, &pt.Choices, &pt.Description)
		if pt.Choices == nil { pt.Choices = []string{} }
		a.Template = &pt
		assignments = append(assignments, a)
	}
	writeJSON(w, 200, map[string]any{"class": c, "properties": assignments})
}

func (a *App) handleCreateClass(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string  `json:"name"`
		Description string  `json:"description"`
		ParentID    *string `json:"parent_id"`
		Icon        string  `json:"icon"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" { writeError(w, 400, "name required"); return }
	if body.Icon == "" { body.Icon = "📄" }
	var c DocumentClass
	err := a.db.QueryRow(context.Background(),
		`INSERT INTO document_classes (id,name,description,parent_id,icon) VALUES ($1,$2,$3,$4,$5)
		 RETURNING id,name,description,parent_id,icon,created_at,updated_at`,
		uuid.New().String(), body.Name, body.Description, body.ParentID, body.Icon).
		Scan(&c.ID, &c.Name, &c.Description, &c.ParentID, &c.Icon, &c.CreatedAt, &c.UpdatedAt)
	if err != nil { writeError(w, 500, err.Error()); return }
	writeJSON(w, 201, c)
}

func (a *App) handleUpdateClass(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		ParentID    *string `json:"parent_id"`
		Icon        *string `json:"icon"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	var c DocumentClass
	err := a.db.QueryRow(context.Background(),
		`UPDATE document_classes SET name=COALESCE($1,name),description=COALESCE($2,description),parent_id=COALESCE($3,parent_id),icon=COALESCE($4,icon),updated_at=NOW()
		 WHERE id=$5 RETURNING id,name,description,parent_id,icon,created_at,updated_at`,
		body.Name, body.Description, body.ParentID, body.Icon, id).
		Scan(&c.ID, &c.Name, &c.Description, &c.ParentID, &c.Icon, &c.CreatedAt, &c.UpdatedAt)
	if err != nil { writeError(w, 404, "class not found"); return }
	writeJSON(w, 200, c)
}

func (a *App) handleDeleteClass(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var count int
	a.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM documents WHERE class_id=$1`, id).Scan(&count)
	if count > 0 {
		writeError(w, 409, fmt.Sprintf("cannot delete: %d document(s) still use this class", count))
		return
	}
	ct, err := a.db.Exec(context.Background(), `DELETE FROM document_classes WHERE id=$1`, id)
	if err != nil || ct.RowsAffected() == 0 {
		writeError(w, 404, "class not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"success": true})
}

func (a *App) handleDeletePropertyTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ct, err := a.db.Exec(context.Background(), `DELETE FROM property_templates WHERE id=$1`, id)
	if err != nil || ct.RowsAffected() == 0 {
		writeError(w, 404, "template not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"success": true})
}

func (a *App) handleListPropertyTemplates(w http.ResponseWriter, r *http.Request) {
	rows, _ := a.db.Query(context.Background(),
		`SELECT id,name,display_name,data_type,is_required,is_multivalued,default_value,choices,description FROM property_templates ORDER BY display_name`)
	defer rows.Close()
	templates := []PropertyTemplate{}
	for rows.Next() {
		var pt PropertyTemplate
		rows.Scan(&pt.ID, &pt.Name, &pt.DisplayName, &pt.DataType, &pt.IsRequired, &pt.IsMultivalued, &pt.DefaultValue, &pt.Choices, &pt.Description)
		if pt.Choices == nil { pt.Choices = []string{} }
		templates = append(templates, pt)
	}
	writeJSON(w, 200, templates)
}

func (a *App) handleCreatePropertyTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          string   `json:"name"`
		DisplayName   string   `json:"display_name"`
		DataType      string   `json:"data_type"`
		IsRequired    bool     `json:"is_required"`
		IsMultivalued bool     `json:"is_multivalued"`
		DefaultValue  *string  `json:"default_value"`
		Choices       []string `json:"choices"`
		Description   string   `json:"description"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" || body.DisplayName == "" || body.DataType == "" {
		writeError(w, 400, "name, display_name and data_type required"); return
	}
	var pt PropertyTemplate
	err := a.db.QueryRow(context.Background(),
		`INSERT INTO property_templates (id,name,display_name,data_type,is_required,is_multivalued,default_value,choices,description)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id,name,display_name,data_type,is_required,is_multivalued,default_value,choices,description`,
		uuid.New().String(), body.Name, body.DisplayName, body.DataType,
		body.IsRequired, body.IsMultivalued, body.DefaultValue, body.Choices, body.Description).
		Scan(&pt.ID, &pt.Name, &pt.DisplayName, &pt.DataType, &pt.IsRequired, &pt.IsMultivalued, &pt.DefaultValue, &pt.Choices, &pt.Description)
	if err != nil { writeError(w, 500, err.Error()); return }
	if pt.Choices == nil { pt.Choices = []string{} }
	writeJSON(w, 201, pt)
}

func (a *App) handleAssignProperty(w http.ResponseWriter, r *http.Request) {
	classID := r.PathValue("id")
	var body struct {
		PropertyTemplateID string  `json:"property_template_id"`
		IsRequired         *bool   `json:"is_required"`
		DefaultValue       *string `json:"default_value"`
		SortOrder          int     `json:"sort_order"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	_, err := a.db.Exec(context.Background(),
		`INSERT INTO class_property_assignments (id,class_id,property_template_id,is_required,default_value,sort_order)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (class_id,property_template_id) DO UPDATE SET is_required=EXCLUDED.is_required,default_value=EXCLUDED.default_value,sort_order=EXCLUDED.sort_order`,
		uuid.New().String(), classID, body.PropertyTemplateID, body.IsRequired, body.DefaultValue, body.SortOrder)
	if err != nil { writeError(w, 500, err.Error()); return }
	writeJSON(w, 200, map[string]bool{"success": true})
}

func (a *App) handleRemoveProperty(w http.ResponseWriter, r *http.Request) {
	a.db.Exec(context.Background(),
		`DELETE FROM class_property_assignments WHERE class_id=$1 AND property_template_id=$2`,
		r.PathValue("id"), r.PathValue("propId"))
	writeJSON(w, 200, map[string]bool{"success": true})
}


// ── ACL Helpers ────────────────────────────────────────────────────────────────

// defaultACL creates the standard ACL entries for a newly uploaded document.
// administrators get all ops; users get read+download.
// owner (the uploader) gets the special "owner" operation that supersedes all others.
func (a *App) defaultACL(ctx context.Context, docID string, owner string) {
	allOps := []string{ACLRead, ACLCreate, ACLUpdate, ACLDelete, ACLDownload}
	userOps := []string{ACLRead, ACLDownload}
	a.db.Exec(ctx,
		`INSERT INTO document_acl (id,document_id,principal,operations) VALUES
		   ($1,$2,'group:administrators',$3),
		   ($4,$5,'group:users',$6)
		 ON CONFLICT (document_id,principal) DO NOTHING`,
		uuid.New().String(), docID, allOps,
		uuid.New().String(), docID, userOps)
	if owner != "" && owner != "user" {
		a.db.Exec(ctx,
			`INSERT INTO document_acl (id,document_id,principal,operations)
			 VALUES ($1,$2,$3,$4)
			 ON CONFLICT (document_id,principal) DO UPDATE SET operations=EXCLUDED.operations`,
			uuid.New().String(), docID, "user:"+owner, []string{ACLOwner})
	}
}

// docAllowed returns true if the JWT claims grant the requested operation
// on the given document.
//
// Evaluation order:
//  1. Document ACL: if any of the user's principals have an entry that grants
//     the operation (or has "owner"), allow.
//  2. System-permission fallback: if none of the user's principals appear in
//     the document ACL at all, fall back to the user's group-level system
//     permissions.  This covers documents whose default ACL group entries were
//     never written (e.g. silent INSERT failure) and legacy documents with no
//     ACL rows.  If the user's principals DO appear in the ACL but without the
//     required operation, that is an explicit restriction and we deny.
func (a *App) docAllowed(ctx context.Context, docID string, op string, claims *Claims) bool {
	if claims == nil {
		return false
	}

	// Build principal list for this user: individual + all groups
	principals := []string{"user:" + claims.Username}
	for _, g := range claims.Groups {
		principals = append(principals, "group:"+g)
	}

	// Check document-level ACL — "owner" operation supersedes all specific ops
	var count int
	err := a.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM document_acl
		WHERE document_id=$1
		  AND principal = ANY($2)
		  AND ($3 = ANY(operations) OR 'owner' = ANY(operations))`,
		docID, principals, op).Scan(&count)
	if err == nil && count > 0 {
		return true
	}

	// Fall back to system permission only when none of the user's principals
	// have any ACL row for this document (no explicit restriction recorded).
	var principalRows int
	a.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM document_acl
		WHERE document_id=$1 AND principal = ANY($2)`,
		docID, principals).Scan(&principalRows)
	if principalRows == 0 {
		sysPerm := map[string]string{
			ACLRead: PermDocRead, ACLCreate: PermDocCreate,
			ACLUpdate: PermDocUpdate, ACLDelete: PermDocDelete,
			ACLDownload: PermDocDownload,
		}
		if p, ok := sysPerm[op]; ok {
			return hasPerm(claims, p)
		}
	}
	return false
}

// getACL returns all ACL entries for a document.
func (a *App) getACL(ctx context.Context, docID string) []ACLEntry {
	rows, err := a.db.Query(ctx,
		`SELECT id,document_id,principal,operations FROM document_acl WHERE document_id=$1 ORDER BY principal`,
		docID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var entries []ACLEntry
	for rows.Next() {
		var e ACLEntry
		rows.Scan(&e.ID, &e.DocumentID, &e.Principal, &e.Operations)
		if e.Operations == nil {
			e.Operations = []string{}
		}
		entries = append(entries, e)
	}
	return entries
}

// ── Document Handlers ──────────────────────────────────────────────────────────

func (a *App) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status"); if status == "" { status = "active" }
	query := `SELECT ` + docCols + ` ` + docFrom + ` WHERE d.status=$1`
	args := []any{status}; i := 2
	if cat := q.Get("category"); cat != "" { query += fmt.Sprintf(" AND d.category=$%d", i); args = append(args, cat); i++ }
	if cls := q.Get("class_id"); cls != "" { query += fmt.Sprintf(" AND d.class_id=$%d", i); args = append(args, cls); i++ }
	if s := q.Get("search"); s != "" {
		query += fmt.Sprintf(" AND (d.name ILIKE $%d OR d.description ILIKE $%d OR EXISTS (SELECT 1 FROM unnest(d.tags) t WHERE t ILIKE $%d))", i, i, i)
		args = append(args, "%"+s+"%"); i++
	}
	if t := q.Get("tags"); t != "" { query += fmt.Sprintf(" AND d.tags && $%d", i); args = append(args, strings.Split(t, ",")); i++ }
	if fid := q.Get("folder_id"); fid != "" { query += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM document_folders df WHERE df.document_id=d.id AND df.folder_id=$%d)", i); args = append(args, fid); i++ }
	_ = i
	query += " ORDER BY d.created_at DESC"
	rows, err := a.db.Query(context.Background(), query, args...)
	if err != nil { writeError(w, 500, err.Error()); return }
	defer rows.Close()
	docs := []Document{}
	for rows.Next() {
		if d, err := scanDoc(rows); err == nil { docs = append(docs, d) }
	}
	writeJSON(w, 200, map[string]any{"documents": docs, "total": len(docs)})
}

func advDTColumn(dt string) string {
	switch dt {
	case "string", "user": return "value_string"
	case "integer":        return "value_integer"
	case "decimal":        return "value_decimal"
	case "boolean":        return "value_boolean"
	case "date":           return "value_date"
	case "datetime":       return "value_datetime"
	}
	return ""
}

func advDTCast(dt string) string {
	switch dt {
	case "integer":  return "::bigint"
	case "decimal":  return "::numeric"
	case "date":     return "::date"
	case "datetime": return "::timestamptz"
	}
	return ""
}

func (a *App) handleAdvancedSearch(w http.ResponseWriter, r *http.Request) {
	var req AdvancedSearchRequest
	json.NewDecoder(r.Body).Decode(&req)
	if req.Status == "" { req.Status = "active" }
	if req.Logic != "OR" { req.Logic = "AND" }

	query := `SELECT ` + docCols + ` ` + docFrom + ` WHERE d.status=$1`
	args := []any{req.Status}; i := 2

	if req.ClassID != "" {
		query += fmt.Sprintf(" AND d.class_id=$%d", i); args = append(args, req.ClassID); i++
	}
	if req.Text != "" {
		query += fmt.Sprintf(" AND (d.name ILIKE $%d OR d.description ILIKE $%d OR EXISTS (SELECT 1 FROM unnest(d.tags) t WHERE t ILIKE $%d))", i, i, i)
		args = append(args, "%"+req.Text+"%"); i++
	}
	if req.FolderID != "" {
		query += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM document_folders df WHERE df.document_id=d.id AND df.folder_id=$%d)", i)
		args = append(args, req.FolderID); i++
	}

	if len(req.Conditions) > 0 {
		var parts []string
		for _, cond := range req.Conditions {
			if cond.PropertyID == "" || cond.Operator == "" { continue }
			var dt string
			if err := a.db.QueryRow(context.Background(), `SELECT data_type FROM property_templates WHERE id=$1`, cond.PropertyID).Scan(&dt); err != nil { continue }
			col := advDTColumn(dt)
			if col == "" { continue }
			cast := advDTCast(dt)

			propIdx := i
			var part string
			var extra []any
			ex := fmt.Sprintf("EXISTS (SELECT 1 FROM document_property_values dpv WHERE dpv.document_id=d.id AND dpv.property_template_id=$%d", propIdx)
			nex := fmt.Sprintf("NOT EXISTS (SELECT 1 FROM document_property_values dpv WHERE dpv.document_id=d.id AND dpv.property_template_id=$%d", propIdx)

			switch cond.Operator {
			case "contains":
				part = ex + fmt.Sprintf(" AND dpv.%s ILIKE $%d)", col, propIdx+1)
				extra = []any{cond.PropertyID, "%" + cond.Value + "%"}
			case "not_contains":
				part = nex + fmt.Sprintf(" AND dpv.%s ILIKE $%d)", col, propIdx+1)
				extra = []any{cond.PropertyID, "%" + cond.Value + "%"}
			case "equals":
				part = ex + fmt.Sprintf(" AND dpv.%s = $%d%s)", col, propIdx+1, cast)
				extra = []any{cond.PropertyID, cond.Value}
			case "not_equals":
				part = nex + fmt.Sprintf(" AND dpv.%s = $%d%s)", col, propIdx+1, cast)
				extra = []any{cond.PropertyID, cond.Value}
			case "starts_with":
				part = ex + fmt.Sprintf(" AND dpv.%s ILIKE $%d)", col, propIdx+1)
				extra = []any{cond.PropertyID, cond.Value + "%"}
			case "ends_with":
				part = ex + fmt.Sprintf(" AND dpv.%s ILIKE $%d)", col, propIdx+1)
				extra = []any{cond.PropertyID, "%" + cond.Value}
			case "is_empty":
				part = nex + fmt.Sprintf(" AND dpv.%s IS NOT NULL)", col)
				extra = []any{cond.PropertyID}
			case "is_not_empty":
				part = ex + fmt.Sprintf(" AND dpv.%s IS NOT NULL)", col)
				extra = []any{cond.PropertyID}
			case "gt":
				part = ex + fmt.Sprintf(" AND dpv.%s > $%d%s)", col, propIdx+1, cast)
				extra = []any{cond.PropertyID, cond.Value}
			case "gte":
				part = ex + fmt.Sprintf(" AND dpv.%s >= $%d%s)", col, propIdx+1, cast)
				extra = []any{cond.PropertyID, cond.Value}
			case "lt":
				part = ex + fmt.Sprintf(" AND dpv.%s < $%d%s)", col, propIdx+1, cast)
				extra = []any{cond.PropertyID, cond.Value}
			case "lte":
				part = ex + fmt.Sprintf(" AND dpv.%s <= $%d%s)", col, propIdx+1, cast)
				extra = []any{cond.PropertyID, cond.Value}
			case "between":
				part = ex + fmt.Sprintf(" AND dpv.%s >= $%d%s AND dpv.%s <= $%d%s)", col, propIdx+1, cast, col, propIdx+2, cast)
				extra = []any{cond.PropertyID, cond.Value, cond.Value2}
			case "is_true":
				part = ex + fmt.Sprintf(" AND dpv.%s = TRUE)", col)
				extra = []any{cond.PropertyID}
			case "is_false":
				part = ex + fmt.Sprintf(" AND dpv.%s = FALSE)", col)
				extra = []any{cond.PropertyID}
			case "before":
				part = ex + fmt.Sprintf(" AND dpv.%s < $%d%s)", col, propIdx+1, cast)
				extra = []any{cond.PropertyID, cond.Value}
			case "after":
				part = ex + fmt.Sprintf(" AND dpv.%s > $%d%s)", col, propIdx+1, cast)
				extra = []any{cond.PropertyID, cond.Value}
			}
			if part != "" {
				parts = append(parts, part)
				args = append(args, extra...)
				i += len(extra)
			}
		}
		if len(parts) > 0 {
			joiner := " AND "
			if req.Logic == "OR" { joiner = " OR " }
			query += " AND (" + strings.Join(parts, joiner) + ")"
		}
	}

	query += " ORDER BY d.created_at DESC"
	rows, err := a.db.Query(context.Background(), query, args...)
	if err != nil { writeError(w, 500, err.Error()); return }
	defer rows.Close()
	docs := []Document{}
	for rows.Next() {
		if d, err := scanDoc(rows); err == nil { docs = append(docs, d) }
	}
	writeJSON(w, 200, map[string]any{"documents": docs, "total": len(docs)})
}

func (a *App) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := scanDoc(a.db.QueryRow(context.Background(), `SELECT `+docCols+` `+docFrom+` WHERE d.id=$1`, id))
	if err != nil { writeError(w, 404, "not found"); return }
	if !a.docAllowed(r.Context(), id, ACLRead, claimsFrom(r)) {
		writeError(w, 403, "access denied"); return
	}
	props, _ := a.loadProperties(context.Background(), id)
	d.Properties = props
	vRows, _ := a.db.Query(context.Background(), `SELECT id,document_id,version,s3_key,file_size,uploaded_at,comment FROM document_versions WHERE document_id=$1 ORDER BY version DESC`, id)
	defer vRows.Close()
	versions := []DocVersion{}
	for vRows.Next() { var v DocVersion; vRows.Scan(&v.ID, &v.DocumentID, &v.Version, &v.S3Key, &v.FileSize, &v.UploadedAt, &v.Comment); versions = append(versions, v) }
	aRows, _ := a.db.Query(context.Background(), `SELECT id,document_id,action,actor,details,created_at FROM audit_log WHERE document_id=$1 ORDER BY created_at DESC LIMIT 20`, id)
	defer aRows.Close()
	entries := []AuditEntry{}
	for aRows.Next() { var e AuditEntry; aRows.Scan(&e.ID, &e.DocumentID, &e.Action, &e.Actor, &e.Details, &e.CreatedAt); entries = append(entries, e) }
	acl := a.getACL(r.Context(), id)
	// Only administrators or those with update permission see the ACL
	canManageACL := a.docAllowed(r.Context(), id, ACLUpdate, claimsFrom(r))
	writeJSON(w, 200, map[string]any{
		"id": d.ID, "name": d.Name, "description": d.Description,
		"class_id": d.ClassID, "class_name": d.ClassName, "class_icon": d.ClassIcon,
		"category": d.Category, "tags": d.Tags, "s3_key": d.S3Key, "s3_bucket": d.S3Bucket,
		"file_size": d.FileSize, "mime_type": d.MimeType, "version": d.Version, "status": d.Status,
		"created_at": d.CreatedAt, "updated_at": d.UpdatedAt, "created_by": d.CreatedBy,
		"properties": props, "versions": versions, "audit": entries,
		"acl": acl, "can_manage_acl": canManageACL,
	})
}

func (a *App) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(100 << 20)
	file, header, err := r.FormFile("file")
	if err != nil { writeError(w, 400, "no file"); return }
	defer file.Close()
	data, _ := io.ReadAll(file)

	claims := claimsFrom(r)
	actor := "user"
	if claims != nil { actor = claims.Username }

	id := uuid.New().String()
	name := r.FormValue("name"); if name == "" { name = header.Filename }
	description := r.FormValue("description")
	category := r.FormValue("category"); if category == "" { category = "General" }
	classID := r.FormValue("class_id")
	var tags []string
	if t := r.FormValue("tags"); t != "" { json.Unmarshal([]byte(t), &tags) }
	var propInputs []PropInput
	if p := r.FormValue("properties"); p != "" { json.Unmarshal([]byte(p), &propInputs) }

	mimeType := header.Header.Get("Content-Type")
	s3Key := fmt.Sprintf("documents/%s/v1/%s", id, header.Filename)

	if err := a.s3Put(s3Key, mimeType, data); err != nil { writeError(w, 500, "s3: "+err.Error()); return }

	var classIDPtr *string
	if classID != "" { classIDPtr = &classID }

	_, err = a.db.Exec(context.Background(),
		`INSERT INTO documents (id,name,description,class_id,category,tags,s3_key,s3_bucket,file_size,mime_type,created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id, name, description, classIDPtr, category, tags, s3Key, a.cfg.S3Bucket, header.Size, mimeType, actor)
	if err != nil { writeError(w, 500, "db: "+err.Error()); return }

	d, _ := scanDoc(a.db.QueryRow(context.Background(), `SELECT `+docCols+` `+docFrom+` WHERE d.id=$1`, id))
	if len(propInputs) > 0 { a.savePropertyValues(context.Background(), id, propInputs) }
	a.db.Exec(context.Background(), `INSERT INTO document_versions (id,document_id,version,s3_key,file_size) VALUES ($1,$2,1,$3,$4)`, uuid.New().String(), id, s3Key, header.Size)
	a.defaultACL(context.Background(), id, actor)
	a.db.Exec(context.Background(),
		`INSERT INTO document_folders (document_id, folder_id, filed_by) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
		id, FolderUnfiledID, actor)
	a.audit(id, "upload", actor, map[string]any{"s3_key": s3Key, "size": header.Size})
	writeJSON(w, 201, d)
}

func (a *App) handleUpdateDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	claims := claimsFrom(r); actor := "user"; if claims != nil { actor = claims.Username }
	if !a.docAllowed(r.Context(), id, ACLUpdate, claims) {
		writeError(w, 403, "access denied"); return
	}
	var body struct {
		Name        *string  `json:"name"`
		Description *string  `json:"description"`
		ClassID     *string  `json:"class_id"`
		Category    *string  `json:"category"`
		Tags        []string `json:"tags"`
		Status      *string  `json:"status"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	_, uerr := a.db.Exec(context.Background(),
		`UPDATE documents SET name=COALESCE($1,name),description=COALESCE($2,description),class_id=COALESCE($3,class_id),category=COALESCE($4,category),tags=COALESCE($5,tags),status=COALESCE($6,status),updated_at=NOW(),updated_by=$8 WHERE id=$7`,
		body.Name, body.Description, body.ClassID, body.Category, body.Tags, body.Status, id, actor)
	if uerr != nil { writeError(w, 404, "not found"); return }
	d, _ := scanDoc(a.db.QueryRow(context.Background(), `SELECT `+docCols+` `+docFrom+` WHERE d.id=$1`, id))
	a.audit(id, "update", actor, body)
	writeJSON(w, 200, d)
}

func (a *App) handleSetProperties(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	claims := claimsFrom(r); actor := "user"; if claims != nil { actor = claims.Username }
	if !a.docAllowed(r.Context(), id, ACLUpdate, claims) {
		writeError(w, 403, "access denied"); return
	}
	var inputs []PropInput
	json.NewDecoder(r.Body).Decode(&inputs)
	if r.URL.Query().Get("replace_all") == "true" {
		a.db.Exec(context.Background(), `DELETE FROM document_property_values WHERE document_id=$1`, id)
	} else {
		for _, inp := range inputs {
			a.db.Exec(context.Background(), `DELETE FROM document_property_values WHERE document_id=$1 AND property_template_id=$2`, id, inp.PropertyTemplateID)
		}
	}
	a.savePropertyValues(context.Background(), id, inputs)
	a.db.Exec(context.Background(), `UPDATE documents SET updated_at=NOW(),updated_by=$1 WHERE id=$2`, actor, id)
	a.audit(id, "update_properties", actor, map[string]int{"count": len(inputs)})
	writeJSON(w, 200, map[string]bool{"success": true})
}

func (a *App) handleNewVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.docAllowed(r.Context(), id, ACLUpdate, claimsFrom(r)) {
		writeError(w, 403, "access denied"); return
	}
	r.ParseMultipartForm(100 << 20)
	file, header, err := r.FormFile("file")
	if err != nil { writeError(w, 400, "no file"); return }
	defer file.Close()
	data, _ := io.ReadAll(file)
	claims := claimsFrom(r); actor := "user"; if claims != nil { actor = claims.Username }
	var cur int
	a.db.QueryRow(context.Background(), `SELECT version FROM documents WHERE id=$1`, id).Scan(&cur)
	newVer := cur + 1
	s3Key := fmt.Sprintf("documents/%s/v%d/%s", id, newVer, header.Filename)
	a.s3Put(s3Key, header.Header.Get("Content-Type"), data)
	a.db.Exec(context.Background(), `INSERT INTO document_versions (id,document_id,version,s3_key,file_size,comment) VALUES ($1,$2,$3,$4,$5,$6)`, uuid.New().String(), id, newVer, s3Key, header.Size, r.FormValue("comment"))
	a.db.Exec(context.Background(), `UPDATE documents SET version=$1,s3_key=$2,file_size=$3,updated_at=NOW(),updated_by=$5 WHERE id=$4`, newVer, s3Key, header.Size, id, actor)
	d, _ := scanDoc(a.db.QueryRow(context.Background(), `SELECT `+docCols+` `+docFrom+` WHERE d.id=$1`, id))
	a.audit(id, "new_version", actor, map[string]any{"version": newVer, "s3_key": s3Key})
	writeJSON(w, 200, d)
}

func (a *App) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	claims := claimsFrom(r); actor := "user"; if claims != nil { actor = claims.Username }
	if !a.docAllowed(r.Context(), id, ACLDownload, claims) {
		writeError(w, 403, "access denied"); return
	}
	var s3Key, name, mimeType string
	if err := a.db.QueryRow(context.Background(), `SELECT s3_key,name,mime_type FROM documents WHERE id=$1`, id).Scan(&s3Key, &name, &mimeType); err != nil {
		writeError(w, 404, "not found"); return
	}
	resp, err := a.s3Get(s3Key)
	if err != nil { log.Printf("download error key=%s: %v", s3Key, err); writeError(w, 502, err.Error()); return }
	defer resp.Body.Close()
	parts := strings.Split(s3Key, "/"); originalFilename := parts[len(parts)-1]
	if originalFilename == "" { originalFilename = name }
	ct := resp.Header.Get("Content-Type")
	if ct == "" || ct == "application/octet-stream" { ct = mimeType }
	if ct == "" { ct = "application/octet-stream" }
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, originalFilename))
	if cl := resp.Header.Get("Content-Length"); cl != "" { w.Header().Set("Content-Length", cl) }
	w.WriteHeader(http.StatusOK)
	io.Copy(w, resp.Body)
	a.audit(id, "download", actor, map[string]string{"s3_key": s3Key})
}

func (a *App) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	hard := r.URL.Query().Get("hard") == "true"
	claims := claimsFrom(r); actor := "user"; if claims != nil { actor = claims.Username }
	if !a.docAllowed(r.Context(), id, ACLDelete, claims) {
		writeError(w, 403, "access denied"); return
	}
	var s3Key string
	if err := a.db.QueryRow(context.Background(), `SELECT s3_key FROM documents WHERE id=$1`, id).Scan(&s3Key); err != nil {
		writeError(w, 404, "not found"); return
	}
	action := "soft_delete"
	if hard { a.s3Delete(s3Key); a.db.Exec(context.Background(), `DELETE FROM documents WHERE id=$1`, id); action = "hard_delete" } else {
		a.db.Exec(context.Background(), `UPDATE documents SET status='deleted',updated_at=NOW(),updated_by=$2 WHERE id=$1`, id, actor)
	}
	a.audit(id, action, actor, map[string]string{"s3_key": s3Key})
	writeJSON(w, 200, map[string]bool{"success": true})
}


// GET /documents/{id}/acl
func (a *App) handleGetACL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.docAllowed(r.Context(), id, ACLUpdate, claimsFrom(r)) {
		writeError(w, 403, "access denied"); return
	}
	writeJSON(w, 200, a.getACL(r.Context(), id))
}

// PUT /documents/{id}/acl — full replace of a principal's operations
func (a *App) handleSetACL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.docAllowed(r.Context(), id, ACLUpdate, claimsFrom(r)) {
		writeError(w, 403, "access denied"); return
	}
	var body struct {
		Principal  string   `json:"principal"`
		Operations []string `json:"operations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Principal == "" {
		writeError(w, 400, "principal and operations required"); return
	}
	_, err := a.db.Exec(r.Context(), `
		INSERT INTO document_acl (id,document_id,principal,operations)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (document_id,principal) DO UPDATE SET operations=EXCLUDED.operations`,
		uuid.New().String(), id, body.Principal, body.Operations)
	if err != nil { writeError(w, 500, err.Error()); return }
	actorName := "user"
	if c := claimsFrom(r); c != nil { actorName = c.Username }
	a.audit(id, "acl_update", actorName, body)
	writeJSON(w, 200, a.getACL(r.Context(), id))
}

// DELETE /documents/{id}/acl/{principal} — remove a principal's ACL entry
func (a *App) handleDeleteACL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.docAllowed(r.Context(), id, ACLUpdate, claimsFrom(r)) {
		writeError(w, 403, "access denied"); return
	}
	principal := r.PathValue("principal")
	a.db.Exec(r.Context(), `DELETE FROM document_acl WHERE document_id=$1 AND principal=$2`, id, principal)
	writeJSON(w, 200, a.getACL(r.Context(), id))
}

// POST /documents/{id}/acl/reset — restore default ACL (including owner entry)
func (a *App) handleResetACL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.docAllowed(r.Context(), id, ACLUpdate, claimsFrom(r)) {
		writeError(w, 403, "access denied"); return
	}
	var createdBy string
	a.db.QueryRow(r.Context(), `SELECT created_by FROM documents WHERE id=$1`, id).Scan(&createdBy)
	a.db.Exec(r.Context(), `DELETE FROM document_acl WHERE document_id=$1`, id)
	a.defaultACL(r.Context(), id, createdBy)
	writeJSON(w, 200, a.getACL(r.Context(), id))
}

// ── Annotation handlers ────────────────────────────────────────────────────────

func (a *App) handleListAnnotations(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.docAllowed(r.Context(), id, ACLRead, claimsFrom(r)) {
		writeError(w, 403, "access denied"); return
	}
	var docVersion int
	if vStr := r.URL.Query().Get("version"); vStr != "" {
		fmt.Sscanf(vStr, "%d", &docVersion)
	}
	if docVersion <= 0 {
		a.db.QueryRow(r.Context(), `SELECT version FROM documents WHERE id=$1`, id).Scan(&docVersion)
	}
	if docVersion <= 0 {
		docVersion = 1
	}
	rows, err := a.db.Query(r.Context(),
		`SELECT id, document_id, doc_version, page, annotation_type, x, y, width, height,
		 COALESCE(color,''), COALESCE(content,''), author, created_at
		 FROM document_annotations WHERE document_id=$1 AND doc_version=$2 ORDER BY page, created_at`,
		id, docVersion)
	if err != nil {
		writeError(w, 500, err.Error()); return
	}
	defer rows.Close()
	anns := []Annotation{}
	for rows.Next() {
		var ann Annotation
		rows.Scan(&ann.ID, &ann.DocumentID, &ann.Version, &ann.Page, &ann.Type,
			&ann.X, &ann.Y, &ann.Width, &ann.Height,
			&ann.Color, &ann.Content, &ann.Author, &ann.CreatedAt)
		anns = append(anns, ann)
	}
	writeJSON(w, 200, anns)
}

func (a *App) handleCreateAnnotation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	claims := claimsFrom(r)
	if !a.docAllowed(r.Context(), id, ACLRead, claims) {
		writeError(w, 403, "access denied"); return
	}
	var inp Annotation
	if err := json.NewDecoder(r.Body).Decode(&inp); err != nil {
		writeError(w, 400, "invalid request body"); return
	}
	valid := map[string]bool{"highlight": true, "rectangle": true, "text": true, "redaction": true, "stamp-approved": true, "stamp-rejected": true, "arrow": true}
	if !valid[inp.Type] {
		writeError(w, 400, "invalid annotation type"); return
	}
	if inp.Version <= 0 {
		a.db.QueryRow(r.Context(), `SELECT version FROM documents WHERE id=$1`, id).Scan(&inp.Version)
	}
	if inp.Version <= 0 {
		inp.Version = 1
	}
	var annID string
	var createdAt time.Time
	err := a.db.QueryRow(r.Context(),
		`INSERT INTO document_annotations
		 (document_id, doc_version, page, annotation_type, x, y, width, height, color, content, author)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id, created_at`,
		id, inp.Version, inp.Page, inp.Type, inp.X, inp.Y, inp.Width, inp.Height, inp.Color, inp.Content, claims.Username,
	).Scan(&annID, &createdAt)
	if err != nil {
		writeError(w, 500, err.Error()); return
	}
	inp.ID = annID
	inp.DocumentID = id
	inp.Author = claims.Username
	inp.CreatedAt = createdAt
	writeJSON(w, 201, inp)
}

func (a *App) handleDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	docID := r.PathValue("id")
	annID := r.PathValue("annId")
	claims := claimsFrom(r)
	if !a.docAllowed(r.Context(), docID, ACLRead, claims) {
		writeError(w, 403, "access denied"); return
	}
	var author string
	err := a.db.QueryRow(r.Context(),
		`SELECT author FROM document_annotations WHERE id=$1 AND document_id=$2`, annID, docID).Scan(&author)
	if err != nil {
		writeError(w, 404, "annotation not found"); return
	}
	if author != claims.Username && !hasPerm(claims, PermDocUpdate) {
		writeError(w, 403, "cannot delete another user's annotation"); return
	}
	a.db.Exec(r.Context(), `DELETE FROM document_annotations WHERE id=$1`, annID)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// ── Folder handlers ────────────────────────────────────────────────────────────

func (a *App) handleGetFolderTree(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(),
		`SELECT id, name, parent_id, created_at, created_by FROM folders ORDER BY name`)
	if err != nil { writeError(w, 500, err.Error()); return }
	defer rows.Close()

	all := map[string]*Folder{}
	var order []string
	for rows.Next() {
		f := &Folder{}
		rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.CreatedAt, &f.CreatedBy)
		all[f.ID] = f
		order = append(order, f.ID)
	}
	// Build tree
	var roots []*Folder
	for _, id := range order {
		f := all[id]
		if f.ParentID == nil {
			roots = append(roots, f)
		} else if parent, ok := all[*f.ParentID]; ok {
			parent.Children = append(parent.Children, f)
		}
	}
	writeJSON(w, 200, roots)
}

func (a *App) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string  `json:"name"`
		ParentID *string `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeError(w, 400, "name required"); return
	}
	claims := claimsFrom(r)
	actor := "system"; if claims != nil { actor = claims.Username }
	id := uuid.New().String()
	_, err := a.db.Exec(r.Context(),
		`INSERT INTO folders (id, name, parent_id, created_by) VALUES ($1,$2,$3,$4)`,
		id, body.Name, body.ParentID, actor)
	if err != nil { writeError(w, 500, err.Error()); return }
	f := &Folder{ID: id, Name: body.Name, ParentID: body.ParentID, CreatedBy: actor}
	writeJSON(w, 201, f)
}

func (a *App) handleUpdateFolder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == FolderRootID || id == FolderUnfiledID {
		writeError(w, 400, "cannot rename system folders"); return
	}
	var body struct{ Name string `json:"name"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeError(w, 400, "name required"); return
	}
	_, err := a.db.Exec(r.Context(), `UPDATE folders SET name=$1 WHERE id=$2`, body.Name, id)
	if err != nil { writeError(w, 500, err.Error()); return }
	writeJSON(w, 200, map[string]string{"id": id, "name": body.Name})
}

func (a *App) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == FolderRootID || id == FolderUnfiledID {
		writeError(w, 400, "cannot delete system folders"); return
	}
	a.db.Exec(r.Context(), `DELETE FROM folders WHERE id=$1`, id)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (a *App) handleGetDocumentFolders(w http.ResponseWriter, r *http.Request) {
	docID := r.PathValue("id")
	claims := claimsFrom(r)
	if !a.docAllowed(r.Context(), docID, ACLRead, claims) {
		writeError(w, 403, "access denied"); return
	}
	rows, err := a.db.Query(r.Context(),
		`SELECT f.id, f.name, f.parent_id, f.created_at, f.created_by
		 FROM folders f JOIN document_folders df ON df.folder_id=f.id
		 WHERE df.document_id=$1 ORDER BY f.name`, docID)
	if err != nil { writeError(w, 500, err.Error()); return }
	defer rows.Close()
	var folders []*Folder
	for rows.Next() {
		f := &Folder{}
		rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.CreatedAt, &f.CreatedBy)
		folders = append(folders, f)
	}
	if folders == nil { folders = []*Folder{} }
	writeJSON(w, 200, folders)
}

func (a *App) handleSetDocumentFolders(w http.ResponseWriter, r *http.Request) {
	docID := r.PathValue("id")
	claims := claimsFrom(r)
	if !a.docAllowed(r.Context(), docID, ACLRead, claims) {
		writeError(w, 403, "access denied"); return
	}
	var body struct{ FolderIDs []string `json:"folder_ids"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid body"); return
	}
	actor := ""; if claims != nil { actor = claims.Username }

	// Filter out system folders from the requested list
	var realFolders []string
	for _, fid := range body.FolderIDs {
		if fid != FolderRootID && fid != FolderUnfiledID {
			realFolders = append(realFolders, fid)
		}
	}

	a.db.Exec(r.Context(), `DELETE FROM document_folders WHERE document_id=$1`, docID)
	if len(realFolders) == 0 {
		// No real folders selected — fall back to Unfiled Folder
		a.db.Exec(r.Context(),
			`INSERT INTO document_folders (document_id, folder_id, filed_by) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			docID, FolderUnfiledID, actor)
	} else {
		for _, fid := range realFolders {
			a.db.Exec(r.Context(),
				`INSERT INTO document_folders (document_id, folder_id, filed_by) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
				docID, fid, actor)
		}
	}
	// Respond with updated list
	rows, _ := a.db.Query(r.Context(),
		`SELECT f.id, f.name, f.parent_id, f.created_at, f.created_by
		 FROM folders f JOIN document_folders df ON df.folder_id=f.id
		 WHERE df.document_id=$1 ORDER BY f.name`, docID)
	defer rows.Close()
	var folders []*Folder
	for rows.Next() {
		f := &Folder{}
		rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.CreatedAt, &f.CreatedBy)
		folders = append(folders, f)
	}
	if folders == nil { folders = []*Folder{} }
	writeJSON(w, 200, folders)
}

var mimeToExt = map[string]string{
	"application/msword":                                                               ".doc",
	"application/vnd.ms-excel":                                                        ".xls",
	"application/vnd.ms-powerpoint":                                                   ".ppt",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":         ".docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":               ".xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation":       ".pptx",
	"application/vnd.oasis.opendocument.text":                                         ".odt",
	"application/vnd.oasis.opendocument.spreadsheet":                                  ".ods",
	"application/vnd.oasis.opendocument.presentation":                                 ".odp",
}

var officeExts = map[string]bool{
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true, ".odt": true, ".ods": true, ".odp": true,
}

// ensureOfficeExt appends the correct extension if the name lacks one that Gotenberg recognises.
func ensureOfficeExt(name, mime string) string {
	dot := strings.LastIndex(name, ".")
	if dot >= 0 {
		ext := strings.ToLower(name[dot:])
		if officeExts[ext] {
			return name
		}
	}
	if ext, ok := mimeToExt[mime]; ok {
		return name + ext
	}
	return name
}

// handlePreview converts an Office document to PDF via Gotenberg and streams it inline.
func (a *App) handlePreview(w http.ResponseWriter, r *http.Request) {
	if a.cfg.GotenbergURL == "" {
		writeError(w, 503, "preview conversion service not configured")
		return
	}
	id := r.PathValue("id")
	claims := claimsFrom(r)
	if !a.docAllowed(r.Context(), id, ACLDownload, claims) {
		writeError(w, 403, "access denied")
		return
	}
	var s3Key, name, mimeType string
	if err := a.db.QueryRow(r.Context(),
		`SELECT s3_key, name, COALESCE(mime_type,'') FROM documents WHERE id=$1 AND status='active'`, id).
		Scan(&s3Key, &name, &mimeType); err != nil {
		writeError(w, 404, "document not found")
		return
	}

	// Ensure the filename sent to Gotenberg has a recognized extension.
	// Documents are often stored without extensions; Gotenberg needs one to pick the converter.
	gotenbergName := ensureOfficeExt(name, mimeType)

	// Download file from S3
	s3resp, err := a.s3Get(s3Key)
	if err != nil {
		writeError(w, 502, "storage error: "+err.Error())
		return
	}
	defer s3resp.Body.Close()
	fileData, err := io.ReadAll(s3resp.Body)
	if err != nil {
		writeError(w, 500, "failed to read document")
		return
	}

	// Build multipart body for Gotenberg
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("files", gotenbergName)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if _, err = fw.Write(fileData); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	mw.Close()

	// POST to Gotenberg LibreOffice converter
	convURL := strings.TrimRight(a.cfg.GotenbergURL, "/") + "/forms/libreoffice/convert"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, convURL, &buf)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, 502, "conversion service unavailable")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		writeError(w, 502, fmt.Sprintf("conversion failed (status %d): %s", resp.StatusCode, b))
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.pdf"`, name))
	io.Copy(w, resp.Body)
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	dbOK := a.db.Ping(context.Background()) == nil
	writeJSON(w, 200, map[string]any{"status": "ok", "db": dbOK, "s3": true, "bucket": a.cfg.S3Bucket})
}

func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	var total int; var size int64
	a.db.QueryRow(context.Background(), `SELECT COUNT(*),COALESCE(SUM(file_size),0) FROM documents WHERE status='active'`).Scan(&total, &size)
	catRows, _ := a.db.Query(context.Background(), `SELECT COALESCE(dc.name,'Unclassified'),dc.icon,COUNT(*) FROM documents d LEFT JOIN document_classes dc ON d.class_id=dc.id WHERE d.status='active' GROUP BY dc.name,dc.icon ORDER BY count DESC`)
	defer catRows.Close()
	cats := []map[string]any{}
	for catRows.Next() {
		var name string; var icon *string; var cnt int
		catRows.Scan(&name, &icon, &cnt)
		ic := "📄"; if icon != nil { ic = *icon }
		cats = append(cats, map[string]any{"category": name, "icon": ic, "count": cnt})
	}
	actRows, _ := a.db.Query(context.Background(), `SELECT action,COUNT(*) FROM audit_log WHERE created_at>NOW()-INTERVAL '7 days' GROUP BY action`)
	defer actRows.Close()
	acts := []map[string]any{}
	for actRows.Next() { var ac string; var cnt int; actRows.Scan(&ac, &cnt); acts = append(acts, map[string]any{"action": ac, "count": cnt}) }
	writeJSON(w, 200, map[string]any{"totalDocuments": total, "totalStorageBytes": size, "byCategory": cats, "recentActivity": acts})
}

// ── Router ─────────────────────────────────────────────────────────────────────

func (a *App) buildMux() http.Handler {
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /health",      a.handleHealth)
	mux.HandleFunc("POST /auth/login", a.handleLogin)

	// Auth required
	mux.HandleFunc("GET /auth/me", a.requireAuth("", a.handleMe))
	mux.HandleFunc("GET /stats",   a.requireAuth(PermDocRead, a.handleStats))

	// User management
	mux.HandleFunc("GET /users",         a.requireAuth(PermUserManage, a.handleListUsers))
	mux.HandleFunc("POST /users",        a.requireAuth(PermUserManage, a.handleCreateUser))
	mux.HandleFunc("PUT /users/{id}",    a.requireAuth(PermUserManage, a.handleUpdateUser))
	mux.HandleFunc("DELETE /users/{id}", a.requireAuth(PermUserManage, a.handleDeleteUser))
	mux.HandleFunc("GET /groups",        a.requireAuth(PermUserManage, a.handleListGroups))
	mux.HandleFunc("PUT /groups/{id}",   a.requireAuth(PermUserManage, a.handleUpdateGroup))

	// Taxonomy
	mux.HandleFunc("GET /classes",                             a.requireAuth(PermTaxRead,   a.handleListClasses))
	mux.HandleFunc("GET /classes/{id}",                        a.requireAuth(PermTaxRead,   a.handleGetClass))
	mux.HandleFunc("POST /classes",                            a.requireAuth(PermTaxCreate, a.handleCreateClass))
	mux.HandleFunc("PUT /classes/{id}",                        a.requireAuth(PermTaxUpdate, a.handleUpdateClass))
	mux.HandleFunc("DELETE /classes/{id}",                     a.requireAuth(PermTaxDelete, a.handleDeleteClass))
	mux.HandleFunc("POST /classes/{id}/properties",            a.requireAuth(PermTaxUpdate, a.handleAssignProperty))
	mux.HandleFunc("DELETE /classes/{id}/properties/{propId}", a.requireAuth(PermTaxUpdate, a.handleRemoveProperty))
	mux.HandleFunc("GET /property-templates",                  a.requireAuth(PermTaxRead,   a.handleListPropertyTemplates))
	mux.HandleFunc("POST /property-templates",                 a.requireAuth(PermTaxCreate, a.handleCreatePropertyTemplate))
	mux.HandleFunc("DELETE /property-templates/{id}",          a.requireAuth(PermTaxDelete, a.handleDeletePropertyTemplate))

	// Documents
	mux.HandleFunc("GET /documents",                 a.requireAuth(PermDocRead,     a.handleListDocuments))
	mux.HandleFunc("POST /documents/search",         a.requireAuth(PermDocRead,     a.handleAdvancedSearch))
	mux.HandleFunc("GET /documents/{id}",            a.requireAuth(PermDocRead,     a.handleGetDocument))
	mux.HandleFunc("POST /documents",                a.requireAuth(PermDocCreate,   a.handleUploadDocument))
	mux.HandleFunc("PUT /documents/{id}",            a.requireAuthOrOwner(PermDocUpdate,   a.handleUpdateDocument))
	mux.HandleFunc("PUT /documents/{id}/properties", a.requireAuthOrOwner(PermDocUpdate,   a.handleSetProperties))
	mux.HandleFunc("POST /documents/{id}/version",   a.requireAuthOrOwner(PermDocUpdate,   a.handleNewVersion))
	mux.HandleFunc("GET /documents/{id}/download",   a.requireAuth(PermDocDownload, a.handleDownload))
	mux.HandleFunc("GET /documents/{id}/preview",    a.requireAuth(PermDocDownload, a.handlePreview))
	mux.HandleFunc("DELETE /documents/{id}",         a.requireAuthOrOwner(PermDocDelete,   a.handleDeleteDocument))

	// Document ACL
	mux.HandleFunc("GET /documents/{id}/acl",                a.requireAuthOrOwner(PermDocUpdate, a.handleGetACL))
	mux.HandleFunc("PUT /documents/{id}/acl",                a.requireAuthOrOwner(PermDocUpdate, a.handleSetACL))
	mux.HandleFunc("DELETE /documents/{id}/acl/{principal}", a.requireAuthOrOwner(PermDocUpdate, a.handleDeleteACL))
	mux.HandleFunc("POST /documents/{id}/acl/reset",         a.requireAuthOrOwner(PermDocUpdate, a.handleResetACL))

	// Annotations
	mux.HandleFunc("GET /documents/{id}/annotations",            a.requireAuth(PermDocRead, a.handleListAnnotations))
	mux.HandleFunc("POST /documents/{id}/annotations",           a.requireAuth(PermDocRead, a.handleCreateAnnotation))
	mux.HandleFunc("DELETE /documents/{id}/annotations/{annId}", a.requireAuth(PermDocRead, a.handleDeleteAnnotation))

	// Folders
	mux.HandleFunc("GET /folders",                         a.requireAuth("",                a.handleGetFolderTree))
	mux.HandleFunc("POST /folders",                        a.requireAuth(PermFolderManage,  a.handleCreateFolder))
	mux.HandleFunc("PUT /folders/{id}",                    a.requireAuth(PermFolderManage,  a.handleUpdateFolder))
	mux.HandleFunc("DELETE /folders/{id}",                 a.requireAuth(PermFolderManage,  a.handleDeleteFolder))
	mux.HandleFunc("GET /documents/{id}/folders",          a.requireAuth(PermDocRead,       a.handleGetDocumentFolders))
	mux.HandleFunc("PUT /documents/{id}/folders",          a.requireAuth(PermDocRead, a.handleSetDocumentFolders))

	return corsMiddleware(mux)
}

// ── Main ────────────────────────────────────────────────────────────────────────

func main() {
	cfg := loadConfig()
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s", cfg.PGUser, cfg.PGPassword, cfg.PGHost, cfg.PGPort, cfg.PGDatabase)
	var pool *pgxpool.Pool; var err error
	for i := 0; i < 15; i++ {
		pool, err = pgxpool.New(context.Background(), dsn)
		if err == nil { if pool.Ping(context.Background()) == nil { break } }
		log.Printf("Waiting for DB... (%d/15)", i+1); time.Sleep(2 * time.Second)
	}
	if err != nil { log.Fatalf("DB connect failed: %v", err) }
	log.Println("✅ Connected to PostgreSQL")
	app := &App{cfg: cfg, db: pool}
	if err := app.initDB(); err != nil { log.Fatalf("DB init: %v", err) }
	if err := app.seedDefaults(); err != nil { log.Fatalf("Seed failed: %v", err) }
	if err := app.createBucket(); err != nil { log.Printf("⚠️  Bucket warning: %v", err) }
	log.Printf("🚀 ECMS API on http://0.0.0.0:%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, app.buildMux()); err != nil {
		log.Fatalf("Server: %v", err)
	}
}
