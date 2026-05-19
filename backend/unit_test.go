package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
)

// ── ensureOfficeExt ────────────────────────────────────────────────────────────

func TestEnsureOfficeExt_AlreadyHasDocx(t *testing.T) {
	if got := ensureOfficeExt("report.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"); got != "report.docx" {
		t.Fatalf("got %q, want report.docx", got)
	}
}

func TestEnsureOfficeExt_NoExt_UsesMIME(t *testing.T) {
	if got := ensureOfficeExt("report", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"); got != "report.docx" {
		t.Fatalf("got %q, want report.docx", got)
	}
}

func TestEnsureOfficeExt_VersionedName_UsesMIME(t *testing.T) {
	// "spec-v1.2" — last dot-segment is not an office extension
	if got := ensureOfficeExt("spec-v1.2", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"); got != "spec-v1.2.docx" {
		t.Fatalf("got %q, want spec-v1.2.docx", got)
	}
}

func TestEnsureOfficeExt_UnknownMIME_NoChange(t *testing.T) {
	if got := ensureOfficeExt("somefile", "application/octet-stream"); got != "somefile" {
		t.Fatalf("got %q, want somefile", got)
	}
}

func TestEnsureOfficeExt_AllSupportedMIMETypes(t *testing.T) {
	cases := []struct{ mime, wantExt string }{
		{"application/msword", ".doc"},
		{"application/vnd.ms-excel", ".xls"},
		{"application/vnd.ms-powerpoint", ".ppt"},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"},
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx"},
		{"application/vnd.openxmlformats-officedocument.presentationml.presentation", ".pptx"},
		{"application/vnd.oasis.opendocument.text", ".odt"},
		{"application/vnd.oasis.opendocument.spreadsheet", ".ods"},
		{"application/vnd.oasis.opendocument.presentation", ".odp"},
	}
	for _, c := range cases {
		want := "file" + c.wantExt
		if got := ensureOfficeExt("file", c.mime); got != want {
			t.Errorf("mime=%s: got %q, want %q", c.mime, got, want)
		}
	}
}

// ── hasPerm ────────────────────────────────────────────────────────────────────

func TestHasPerm_Present(t *testing.T) {
	c := &Claims{Permissions: []string{"documents:read", "documents:create"}}
	if !hasPerm(c, "documents:read") {
		t.Fatal("expected true")
	}
}

func TestHasPerm_Absent(t *testing.T) {
	c := &Claims{Permissions: []string{"documents:read"}}
	if hasPerm(c, "documents:delete") {
		t.Fatal("expected false")
	}
}

func TestHasPerm_NilClaims(t *testing.T) {
	if hasPerm(nil, "documents:read") {
		t.Fatal("expected false for nil claims")
	}
}

func TestHasPerm_EmptyPermissions(t *testing.T) {
	if hasPerm(&Claims{}, "documents:read") {
		t.Fatal("expected false for empty permissions")
	}
}

// ── getEnv ─────────────────────────────────────────────────────────────────────

func TestGetEnv_ReturnsValue(t *testing.T) {
	os.Setenv("_ECMS_TEST_VAR", "hello")
	defer os.Unsetenv("_ECMS_TEST_VAR")
	if got := getEnv("_ECMS_TEST_VAR", "fallback"); got != "hello" {
		t.Fatalf("got %q, want hello", got)
	}
}

func TestGetEnv_ReturnsFallback(t *testing.T) {
	os.Unsetenv("_ECMS_TEST_MISSING")
	if got := getEnv("_ECMS_TEST_MISSING", "default"); got != "default" {
		t.Fatalf("got %q, want default", got)
	}
}

// ── writeJSON / writeError ─────────────────────────────────────────────────────

func TestWriteJSON_SetsStatusAndContentType(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 201, map[string]string{"key": "val"})
	if w.Code != 201 {
		t.Fatalf("status %d, want 201", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q, want application/json", ct)
	}
}

func TestWriteError_SetsStatusAndErrorField(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, 404, "not found")
	if w.Code != 404 {
		t.Fatalf("status %d, want 404", w.Code)
	}
	var body map[string]string
	if err := decodeBody(w, &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "not found" {
		t.Fatalf("error field %q, want 'not found'", body["error"])
	}
}

// ── JWT round-trip ─────────────────────────────────────────────────────────────

func TestJWT_RoundTrip(t *testing.T) {
	app := &App{cfg: Config{JWTSecret: "test-secret"}}
	tok, err := app.generateToken("uid-1", "alice", []string{"admins"}, []string{"documents:read"})
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	claims, err := app.parseToken(tok)
	if err != nil {
		t.Fatalf("parseToken: %v", err)
	}
	if claims.Username != "alice" {
		t.Fatalf("username %q, want alice", claims.Username)
	}
	if len(claims.Groups) != 1 || claims.Groups[0] != "admins" {
		t.Fatalf("groups %v, want [admins]", claims.Groups)
	}
}

func TestJWT_WrongSecret(t *testing.T) {
	app := &App{cfg: Config{JWTSecret: "secret-a"}}
	tok, _ := app.generateToken("uid", "bob", nil, nil)
	app2 := &App{cfg: Config{JWTSecret: "secret-b"}}
	if _, err := app2.parseToken(tok); err == nil {
		t.Fatal("expected parse to fail with wrong secret")
	}
}

// decodeBody is a test helper used only in this file.
func decodeBody(w *httptest.ResponseRecorder, v any) error {
	return json.NewDecoder(w.Body).Decode(v)
}
