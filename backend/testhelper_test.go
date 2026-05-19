package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Global test state ─────────────────────────────────────────────────────────

var testApp *App
var testSrv *httptest.Server

// TestMain creates a fresh ecms_test database, wires an in-process S3 mock,
// and starts the full HTTP stack via httptest.NewServer.
//
// Requires a reachable PostgreSQL instance. Set TEST_PG_DSN to override the
// default docker-compose DSN (postgres://postgres:localpassword@localhost:5432/postgres).
func TestMain(m *testing.M) {
	adminDSN := os.Getenv("TEST_PG_DSN")
	if adminDSN == "" {
		adminDSN = "postgres://postgres:localpassword@localhost:5432/postgres"
	}

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil || adminPool.Ping(ctx) != nil {
		fmt.Println("⚠️  Skipping integration tests: postgres not reachable")
		os.Exit(0)
	}
	defer adminPool.Close()

	adminPool.Exec(ctx, "DROP DATABASE IF EXISTS ecms_test")
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE ecms_test"); err != nil {
		fmt.Println("cannot create ecms_test:", err)
		os.Exit(1)
	}

	// Replace only the database-name segment (last path component) in the DSN.
	lastSlash := strings.LastIndex(adminDSN, "/")
	testDSN := adminDSN[:lastSlash+1] + "ecms_test"
	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		fmt.Println("cannot connect to ecms_test:", err)
		os.Exit(1)
	}
	defer pool.Close()

	s3 := newS3Mock()
	defer s3.close()

	testApp = &App{
		cfg: Config{
			JWTSecret:  "test-jwt-secret",
			AWSRegion:  "us-east-1",
			AWSKeyID:   "testkey",
			AWSSecret:  "testsecret",
			S3Bucket:   "ecms-test",
			S3Endpoint: s3.baseURL(),
		},
		db: pool,
	}
	if err := testApp.initDB(); err != nil {
		fmt.Println("initDB:", err)
		os.Exit(1)
	}
	if err := testApp.seedDefaults(); err != nil {
		fmt.Println("seedDefaults:", err)
		os.Exit(1)
	}

	testSrv = httptest.NewServer(testApp.buildMux())
	defer testSrv.Close()

	os.Exit(m.Run())
}

// ── S3 mock ────────────────────────────────────────────────────────────────────

type s3Mock struct {
	mu      sync.RWMutex
	objects map[string][]byte
	srv     *httptest.Server
}

func newS3Mock() *s3Mock {
	m := &s3Mock{objects: make(map[string][]byte)}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	return m
}

func (m *s3Mock) handle(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Path
	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.objects[key] = body
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		m.mu.RLock()
		data, ok := m.objects[key]
		m.mu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(data)
	case http.MethodDelete:
		m.mu.Lock()
		delete(m.objects, key)
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case http.MethodHead:
		w.WriteHeader(http.StatusOK)
	}
}

func (m *s3Mock) baseURL() string { return m.srv.URL }
func (m *s3Mock) close()          { m.srv.Close() }

// ── HTTP helpers ───────────────────────────────────────────────────────────────

// apiDo sends a JSON request to the test server and returns the response.
func apiDo(t *testing.T, method, path string, body any, token string) *http.Response {
	t.Helper()
	var bodyR io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyR = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, testSrv.URL+path, bodyR)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}
	return resp
}

// mustDecode decodes the response body as JSON into v.
func mustDecode(t *testing.T, r io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(r).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// mustStatus asserts the response status and prints the body on failure.
func mustStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("HTTP %d (want %d): %s", resp.StatusCode, want, b)
	}
}

// adminToken fetches a fresh JWT for the built-in admin account.
func adminToken(t *testing.T) string {
	t.Helper()
	resp := apiDo(t, "POST", "/auth/login",
		map[string]any{"username": "admin", "password": "admin"}, "")
	mustStatus(t, resp, 200)
	var body map[string]any
	mustDecode(t, resp.Body, &body)
	tok, _ := body["token"].(string)
	if tok == "" {
		t.Fatal("login returned no token")
	}
	return tok
}

// uploadDoc creates a document via multipart POST and returns its ID.
func uploadDoc(t *testing.T, token, filename, content string) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	fw.Write([]byte(content))
	mw.WriteField("name", filename)
	mw.Close()

	req, _ := http.NewRequest("POST", testSrv.URL+"/documents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	mustStatus(t, resp, 201)
	var body map[string]any
	mustDecode(t, resp.Body, &body)
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatal("upload returned no id")
	}
	return id
}

// ── Unique name helper ────────────────────────────────────────────────────────

var uidCounter atomic.Int64

// uid returns a monotonically increasing integer string for unique test names.
func uid() string { return fmt.Sprintf("%d", uidCounter.Add(1)) }
