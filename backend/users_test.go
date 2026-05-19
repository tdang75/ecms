package main

import (
	"testing"
)

func TestUsers_RequiresUserManage(t *testing.T) {
	// No token → 401
	mustStatus(t, apiDo(t, "GET", "/users", nil, ""), 401)
}

func TestListUsers(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "GET", "/users", nil, tok)
	mustStatus(t, resp, 200)
	var users []any
	mustDecode(t, resp.Body, &users)
	if len(users) == 0 {
		t.Fatal("expected at least the seeded admin user")
	}
}

func TestCreateUser(t *testing.T) {
	tok := adminToken(t)
	uname := "user-" + uid()
	resp := apiDo(t, "POST", "/users", map[string]any{
		"username": uname,
		"email":    uname + "@test.local",
		"password": "securepass123",
		"groups":   []string{"users"},
	}, tok)
	mustStatus(t, resp, 201)
	var body map[string]any
	mustDecode(t, resp.Body, &body)
	if body["username"] != uname {
		t.Fatalf("username %v, want %v", body["username"], uname)
	}
}

func TestCreateUser_DuplicateUsername(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "POST", "/users", map[string]any{
		"username": "admin",
		"email":    "dup-" + uid() + "@test.local",
		"password": "pass",
	}, tok)
	mustStatus(t, resp, 409)
}

func TestCreateUser_MissingFields(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "POST", "/users",
		map[string]any{"username": "incomplete"}, tok)
	mustStatus(t, resp, 400)
}

func TestUpdateUser(t *testing.T) {
	tok := adminToken(t)
	uname := "update-user-" + uid()
	r1 := apiDo(t, "POST", "/users", map[string]any{
		"username": uname, "email": uname + "@test.local", "password": "pass1",
	}, tok)
	mustStatus(t, r1, 201)
	var created map[string]any
	mustDecode(t, r1.Body, &created)
	id := created["id"].(string)

	newEmail := "new-" + uid() + "@test.local"
	r2 := apiDo(t, "PUT", "/users/"+id, map[string]any{"email": newEmail}, tok)
	mustStatus(t, r2, 200)
}

func TestDeleteUser(t *testing.T) {
	tok := adminToken(t)
	uname := "del-user-" + uid()
	r1 := apiDo(t, "POST", "/users", map[string]any{
		"username": uname, "email": uname + "@test.local", "password": "pass1",
	}, tok)
	mustStatus(t, r1, 201)
	var created map[string]any
	mustDecode(t, r1.Body, &created)
	id := created["id"].(string)

	r2 := apiDo(t, "DELETE", "/users/"+id, nil, tok)
	mustStatus(t, r2, 200)
}

func TestDeleteAdminUser_Protected(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "DELETE", "/users/00000000-0000-0000-0002-000000000001", nil, tok)
	mustStatus(t, resp, 403)
}

func TestRegularUser_CannotManageUsers(t *testing.T) {
	tok := adminToken(t)
	uname := "regular-" + uid()
	apiDo(t, "POST", "/users", map[string]any{
		"username": uname, "email": uname + "@test.local",
		"password": "pass123", "groups": []string{"users"},
	}, tok)

	// Login as the regular user
	r := apiDo(t, "POST", "/auth/login",
		map[string]any{"username": uname, "password": "pass123"}, "")
	mustStatus(t, r, 200)
	var loginBody map[string]any
	mustDecode(t, r.Body, &loginBody)
	userTok := loginBody["token"].(string)

	// Must be forbidden
	mustStatus(t, apiDo(t, "GET", "/users", nil, userTok), 403)
	mustStatus(t, apiDo(t, "GET", "/groups", nil, userTok), 403)
}

func TestListGroups(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "GET", "/groups", nil, tok)
	mustStatus(t, resp, 200)
	var groups []any
	mustDecode(t, resp.Body, &groups)
	if len(groups) < 2 {
		t.Fatalf("expected ≥2 seeded groups, got %d", len(groups))
	}
}

func TestUpdateGroup(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "GET", "/groups", nil, tok)
	mustStatus(t, resp, 200)
	var groups []map[string]any
	mustDecode(t, resp.Body, &groups)

	// Find the "users" group
	var usersGroupID string
	for _, g := range groups {
		if g["name"] == "users" {
			usersGroupID = g["id"].(string)
			break
		}
	}
	if usersGroupID == "" {
		t.Fatal("users group not found")
	}

	resp2 := apiDo(t, "PUT", "/groups/"+usersGroupID, map[string]any{
		"description": "Standard users (updated by test)",
	}, tok)
	mustStatus(t, resp2, 200)
}
