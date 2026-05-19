package main

import (
	"testing"
)

func TestTaxonomy_RequiresAuth(t *testing.T) {
	resp := apiDo(t, "GET", "/classes", nil, "")
	mustStatus(t, resp, 401)
}

// ── Document classes ──────────────────────────────────────────────────────────

func TestListClasses_HasSeededData(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "GET", "/classes", nil, tok)
	mustStatus(t, resp, 200)
	var classes []any
	mustDecode(t, resp.Body, &classes)
	if len(classes) < 6 {
		t.Fatalf("expected ≥6 seeded classes, got %d", len(classes))
	}
}

func TestCreateClass(t *testing.T) {
	tok := adminToken(t)
	name := "TestClass-" + uid()
	resp := apiDo(t, "POST", "/classes",
		map[string]any{"name": name, "description": "a test class", "icon": "🧪"}, tok)
	mustStatus(t, resp, 201)
	var created map[string]any
	mustDecode(t, resp.Body, &created)
	if created["name"] != name {
		t.Fatalf("name %v, want %v", created["name"], name)
	}
	if created["id"] == "" {
		t.Fatal("no id in created class")
	}
}

func TestCreateClass_MissingName(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "POST", "/classes", map[string]any{"icon": "📄"}, tok)
	mustStatus(t, resp, 400)
}

func TestGetClass_WithProperties(t *testing.T) {
	tok := adminToken(t)

	// Create a class
	name := "ClassWithProps-" + uid()
	r1 := apiDo(t, "POST", "/classes", map[string]any{"name": name, "icon": "🗂"}, tok)
	mustStatus(t, r1, 201)
	var cls map[string]any
	mustDecode(t, r1.Body, &cls)
	clsID := cls["id"].(string)

	// Assign the seeded "author" property template
	authorTemplateID := "10000000-0000-0000-0000-000000000001"
	r2 := apiDo(t, "POST", "/classes/"+clsID+"/properties",
		map[string]any{"property_template_id": authorTemplateID, "sort_order": 1}, tok)
	mustStatus(t, r2, 200)

	// Get class — should include the assignment
	r3 := apiDo(t, "GET", "/classes/"+clsID, nil, tok)
	mustStatus(t, r3, 200)
	var detail map[string]any
	mustDecode(t, r3.Body, &detail)
	props, _ := detail["properties"].([]any)
	if len(props) == 0 {
		t.Fatal("expected at least one property assignment")
	}
}

func TestUpdateClass(t *testing.T) {
	tok := adminToken(t)
	name := "UpdateMe-" + uid()
	r1 := apiDo(t, "POST", "/classes", map[string]any{"name": name, "icon": "📝"}, tok)
	mustStatus(t, r1, 201)
	var cls map[string]any
	mustDecode(t, r1.Body, &cls)
	id := cls["id"].(string)

	newName := "Updated-" + uid()
	r2 := apiDo(t, "PUT", "/classes/"+id, map[string]any{"name": newName}, tok)
	mustStatus(t, r2, 200)
	var updated map[string]any
	mustDecode(t, r2.Body, &updated)
	if updated["name"] != newName {
		t.Fatalf("name %v, want %v", updated["name"], newName)
	}
}

func TestDeleteClass(t *testing.T) {
	tok := adminToken(t)
	r1 := apiDo(t, "POST", "/classes",
		map[string]any{"name": "DeleteMe-" + uid(), "icon": "🗑"}, tok)
	mustStatus(t, r1, 201)
	var cls map[string]any
	mustDecode(t, r1.Body, &cls)
	id := cls["id"].(string)

	r2 := apiDo(t, "DELETE", "/classes/"+id, nil, tok)
	mustStatus(t, r2, 200)
}

// ── Property templates ────────────────────────────────────────────────────────

func TestListTemplates_HasSeededData(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "GET", "/property-templates", nil, tok)
	mustStatus(t, resp, 200)
	var templates []any
	mustDecode(t, resp.Body, &templates)
	if len(templates) < 14 {
		t.Fatalf("expected ≥14 seeded templates, got %d", len(templates))
	}
}

func TestCreatePropertyTemplate(t *testing.T) {
	tok := adminToken(t)
	name := "prop_" + uid()
	resp := apiDo(t, "POST", "/property-templates", map[string]any{
		"name": name, "display_name": "Test Property " + uid(), "data_type": "string",
	}, tok)
	mustStatus(t, resp, 201)
	var pt map[string]any
	mustDecode(t, resp.Body, &pt)
	if pt["name"] != name {
		t.Fatalf("name %v, want %v", pt["name"], name)
	}
}

func TestCreatePropertyTemplate_InvalidDataType(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "POST", "/property-templates", map[string]any{
		"name": "bad_" + uid(), "display_name": "Bad Type", "data_type": "invalid",
	}, tok)
	// DB constraint rejects invalid data_type
	if resp.StatusCode == 201 {
		t.Fatal("expected non-201 for invalid data_type")
	}
}

func TestCreatePropertyTemplate_MissingFields(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "POST", "/property-templates",
		map[string]any{"name": "incomplete_" + uid()}, tok)
	mustStatus(t, resp, 400)
}

func TestAssignAndRemoveProperty(t *testing.T) {
	tok := adminToken(t)

	r1 := apiDo(t, "POST", "/classes",
		map[string]any{"name": "AssignTest-" + uid(), "icon": "📎"}, tok)
	mustStatus(t, r1, 201)
	var cls map[string]any
	mustDecode(t, r1.Body, &cls)
	clsID := cls["id"].(string)

	templateID := "10000000-0000-0000-0000-000000000001"
	r2 := apiDo(t, "POST", "/classes/"+clsID+"/properties",
		map[string]any{"property_template_id": templateID, "sort_order": 1}, tok)
	mustStatus(t, r2, 200)

	r3 := apiDo(t, "DELETE", "/classes/"+clsID+"/properties/"+templateID, nil, tok)
	mustStatus(t, r3, 200)
}
