package main

import (
	"testing"
)

func TestLogin_Success(t *testing.T) {
	resp := apiDo(t, "POST", "/auth/login",
		map[string]any{"username": "admin", "password": "admin"}, "")
	mustStatus(t, resp, 200)

	var body map[string]any
	mustDecode(t, resp.Body, &body)

	if _, ok := body["token"]; !ok {
		t.Fatal("response missing token field")
	}
	perms, _ := body["permissions"].([]any)
	if len(perms) == 0 {
		t.Fatal("response missing permissions")
	}
	groups, _ := body["groups"].([]any)
	if len(groups) == 0 {
		t.Fatal("response missing groups")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	resp := apiDo(t, "POST", "/auth/login",
		map[string]any{"username": "admin", "password": "wrongpassword"}, "")
	mustStatus(t, resp, 401)
}

func TestLogin_UnknownUser(t *testing.T) {
	resp := apiDo(t, "POST", "/auth/login",
		map[string]any{"username": "nobody", "password": "pass"}, "")
	mustStatus(t, resp, 401)
}

func TestLogin_EmptyBody(t *testing.T) {
	resp := apiDo(t, "POST", "/auth/login", map[string]any{}, "")
	mustStatus(t, resp, 400)
}

func TestLogin_MissingPassword(t *testing.T) {
	resp := apiDo(t, "POST", "/auth/login",
		map[string]any{"username": "admin"}, "")
	mustStatus(t, resp, 400)
}

func TestMe_Authenticated(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "GET", "/auth/me", nil, tok)
	mustStatus(t, resp, 200)

	var body map[string]any
	mustDecode(t, resp.Body, &body)
	if body["username"] != "admin" {
		t.Fatalf("username %v, want admin", body["username"])
	}
}

func TestMe_NoToken(t *testing.T) {
	resp := apiDo(t, "GET", "/auth/me", nil, "")
	mustStatus(t, resp, 401)
}

func TestMe_InvalidToken(t *testing.T) {
	resp := apiDo(t, "GET", "/auth/me", nil, "not.a.valid.jwt")
	mustStatus(t, resp, 401)
}

func TestHealth_NoAuth(t *testing.T) {
	resp := apiDo(t, "GET", "/health", nil, "")
	mustStatus(t, resp, 200)
	var body map[string]any
	mustDecode(t, resp.Body, &body)
	if body["status"] != "ok" {
		t.Fatalf("status %v, want ok", body["status"])
	}
	if db, _ := body["db"].(bool); !db {
		t.Fatal("health: db not ok")
	}
}
