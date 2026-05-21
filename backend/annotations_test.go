package main

import (
	"testing"
)

// ── Annotations ───────────────────────────────────────────────────────────────

func TestAnnotations_CreateListDelete(t *testing.T) {
	tok := adminToken(t)
	docID := uploadDoc(t, tok, "ann-doc-"+uid()+".pdf", "%PDF-1.4 dummy")

	// Create
	resp := apiDo(t, "POST", "/documents/"+docID+"/annotations", map[string]any{
		"page": 1, "type": "highlight",
		"x": 0.1, "y": 0.2, "width": 0.3, "height": 0.05,
		"color": "#ffeb3b",
	}, tok)
	mustStatus(t, resp, 201)
	var ann map[string]any
	mustDecode(t, resp.Body, &ann)
	annID, _ := ann["id"].(string)
	if annID == "" {
		t.Fatal("no annotation id in response")
	}
	if ann["type"] != "highlight" {
		t.Fatalf("type %v, want highlight", ann["type"])
	}
	if ann["author"] != "admin" {
		t.Fatalf("author %v, want admin", ann["author"])
	}

	// List — should contain the new annotation
	resp2 := apiDo(t, "GET", "/documents/"+docID+"/annotations", nil, tok)
	mustStatus(t, resp2, 200)
	var anns []any
	mustDecode(t, resp2.Body, &anns)
	found := false
	for _, a := range anns {
		am, _ := a.(map[string]any)
		if am["id"] == annID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("created annotation not found in list")
	}

	// Delete
	resp3 := apiDo(t, "DELETE", "/documents/"+docID+"/annotations/"+annID, nil, tok)
	mustStatus(t, resp3, 200)

	// Verify gone
	resp4 := apiDo(t, "GET", "/documents/"+docID+"/annotations", nil, tok)
	var annsAfter []any
	mustDecode(t, resp4.Body, &annsAfter)
	for _, a := range annsAfter {
		am, _ := a.(map[string]any)
		if am["id"] == annID {
			t.Fatal("annotation still present after delete")
		}
	}
}

func TestAnnotations_AllTypes(t *testing.T) {
	tok := adminToken(t)
	docID := uploadDoc(t, tok, "ann-types-"+uid()+".pdf", "%PDF-1.4")

	for _, atype := range []string{"highlight", "rectangle", "text", "redaction", "stamp-approved", "stamp-rejected", "arrow"} {
		resp := apiDo(t, "POST", "/documents/"+docID+"/annotations", map[string]any{
			"page": 1, "type": atype,
			"x": 0.0, "y": 0.0, "width": 0.1, "height": 0.1,
		}, tok)
		mustStatus(t, resp, 201)
	}
}

func TestAnnotations_InvalidType(t *testing.T) {
	tok := adminToken(t)
	docID := uploadDoc(t, tok, "ann-bad-"+uid()+".pdf", "%PDF-1.4")

	resp := apiDo(t, "POST", "/documents/"+docID+"/annotations", map[string]any{
		"page": 1, "type": "freehand",
		"x": 0.0, "y": 0.0, "width": 0.1, "height": 0.1,
	}, tok)
	mustStatus(t, resp, 400)
}

func TestAnnotations_VersionIsolation(t *testing.T) {
	tok := adminToken(t)
	docID := uploadDoc(t, tok, "ann-ver-"+uid()+".pdf", "%PDF-1.4")

	// Create annotation for version 1 (explicit)
	resp1 := apiDo(t, "POST", "/documents/"+docID+"/annotations", map[string]any{
		"page": 1, "type": "highlight", "version": 1,
		"x": 0.1, "y": 0.1, "width": 0.2, "height": 0.05,
	}, tok)
	mustStatus(t, resp1, 201)

	// Create annotation for version 2
	resp2 := apiDo(t, "POST", "/documents/"+docID+"/annotations", map[string]any{
		"page": 1, "type": "rectangle", "version": 2,
		"x": 0.2, "y": 0.2, "width": 0.2, "height": 0.05,
	}, tok)
	mustStatus(t, resp2, 201)
	var ann2 map[string]any
	mustDecode(t, resp2.Body, &ann2)
	ann2ID, _ := ann2["id"].(string)

	// GET ?version=1 should not contain the v2 annotation
	r1 := apiDo(t, "GET", "/documents/"+docID+"/annotations?version=1", nil, tok)
	mustStatus(t, r1, 200)
	var anns1 []any
	mustDecode(t, r1.Body, &anns1)
	for _, a := range anns1 {
		am, _ := a.(map[string]any)
		if am["id"] == ann2ID {
			t.Fatal("v2 annotation appeared in v1 results")
		}
	}

	// GET ?version=2 should contain the v2 annotation
	r2 := apiDo(t, "GET", "/documents/"+docID+"/annotations?version=2", nil, tok)
	mustStatus(t, r2, 200)
	var anns2 []any
	mustDecode(t, r2.Body, &anns2)
	found := false
	for _, a := range anns2 {
		am, _ := a.(map[string]any)
		if am["id"] == ann2ID {
			found = true
		}
	}
	if !found {
		t.Fatal("v2 annotation not found in v2 results")
	}
}

func TestAnnotations_DeleteNotFound(t *testing.T) {
	tok := adminToken(t)
	docID := uploadDoc(t, tok, "ann-del-"+uid()+".pdf", "%PDF-1.4")

	resp := apiDo(t, "DELETE",
		"/documents/"+docID+"/annotations/00000000-0000-0000-0000-000000000000", nil, tok)
	mustStatus(t, resp, 404)
}

func TestAnnotations_RequiresAuth(t *testing.T) {
	tok := adminToken(t)
	docID := uploadDoc(t, tok, "ann-auth-"+uid()+".pdf", "%PDF-1.4")

	resp := apiDo(t, "GET", "/documents/"+docID+"/annotations", nil, "")
	mustStatus(t, resp, 401)
}

// ── ACL ───────────────────────────────────────────────────────────────────────

func TestACL_DefaultEntries(t *testing.T) {
	tok := adminToken(t)
	docID := uploadDoc(t, tok, "acl-default-"+uid()+".txt", "content")

	resp := apiDo(t, "GET", "/documents/"+docID+"/acl", nil, tok)
	mustStatus(t, resp, 200)
	var acl []any
	mustDecode(t, resp.Body, &acl)
	if len(acl) == 0 {
		t.Fatal("expected default ACL entries after upload")
	}
	// Verify administrators and users groups are present
	found := map[string]bool{}
	for _, e := range acl {
		em, _ := e.(map[string]any)
		found[em["principal"].(string)] = true
	}
	if !found["group:administrators"] {
		t.Fatal("group:administrators not in default ACL")
	}
	if !found["group:users"] {
		t.Fatal("group:users not in default ACL")
	}
}

func TestACL_SetEntry(t *testing.T) {
	tok := adminToken(t)
	docID := uploadDoc(t, tok, "acl-set-"+uid()+".txt", "content")

	resp := apiDo(t, "PUT", "/documents/"+docID+"/acl", map[string]any{
		"principal":  "user:testuser",
		"operations": []string{"read", "download"},
	}, tok)
	mustStatus(t, resp, 200)
	var acl []any
	mustDecode(t, resp.Body, &acl)
	found := false
	for _, e := range acl {
		em, _ := e.(map[string]any)
		if em["principal"] == "user:testuser" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("custom ACL entry not found after set")
	}
}

func TestACL_DeleteEntry(t *testing.T) {
	tok := adminToken(t)
	docID := uploadDoc(t, tok, "acl-del-"+uid()+".txt", "content")

	// Set then delete
	apiDo(t, "PUT", "/documents/"+docID+"/acl", map[string]any{
		"principal": "user:todelete", "operations": []string{"read"},
	}, tok)

	resp := apiDo(t, "DELETE", "/documents/"+docID+"/acl/user:todelete", nil, tok)
	mustStatus(t, resp, 200)

	var acl []any
	mustDecode(t, resp.Body, &acl)
	for _, e := range acl {
		em, _ := e.(map[string]any)
		if em["principal"] == "user:todelete" {
			t.Fatal("entry still present after delete")
		}
	}
}

func TestACL_Reset(t *testing.T) {
	tok := adminToken(t)
	docID := uploadDoc(t, tok, "acl-reset-"+uid()+".txt", "content")

	// Add a custom entry
	apiDo(t, "PUT", "/documents/"+docID+"/acl", map[string]any{
		"principal": "user:custom", "operations": []string{"read"},
	}, tok)

	resp := apiDo(t, "POST", "/documents/"+docID+"/acl/reset", nil, tok)
	mustStatus(t, resp, 200)
	var acl []any
	mustDecode(t, resp.Body, &acl)

	// After reset, only default entries should exist
	for _, e := range acl {
		em, _ := e.(map[string]any)
		if em["principal"] == "user:custom" {
			t.Fatal("custom entry survived reset")
		}
	}
	if len(acl) < 2 {
		t.Fatalf("expected ≥2 default entries after reset, got %d", len(acl))
	}
}
