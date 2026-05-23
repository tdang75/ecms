package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"testing"
)

func TestDocuments_RequiresAuth(t *testing.T) {
	resp := apiDo(t, "GET", "/documents", nil, "")
	mustStatus(t, resp, 401)
}

func TestUploadDocument(t *testing.T) {
	tok := adminToken(t)
	id := uploadDoc(t, tok, "upload-test-"+uid()+".txt", "hello world")
	if id == "" {
		t.Fatal("no document id returned")
	}
}

func TestListDocuments_ContainsUploaded(t *testing.T) {
	tok := adminToken(t)
	name := "list-test-" + uid() + ".txt"
	id := uploadDoc(t, tok, name, "content")

	resp := apiDo(t, "GET", "/documents", nil, tok)
	mustStatus(t, resp, 200)
	var body map[string]any
	mustDecode(t, resp.Body, &body)

	docs, _ := body["documents"].([]any)
	for _, d := range docs {
		dm, _ := d.(map[string]any)
		if dm["id"] == id {
			return
		}
	}
	t.Fatalf("uploaded document %s not found in list", id)
}

func TestGetDocument(t *testing.T) {
	tok := adminToken(t)
	id := uploadDoc(t, tok, "get-test-"+uid()+".txt", "data")

	resp := apiDo(t, "GET", "/documents/"+id, nil, tok)
	mustStatus(t, resp, 200)
	var body map[string]any
	mustDecode(t, resp.Body, &body)
	if body["id"] != id {
		t.Fatalf("id %v, want %v", body["id"], id)
	}
}

func TestGetDocument_NotFound(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "GET", "/documents/00000000-0000-0000-0000-000000000000", nil, tok)
	mustStatus(t, resp, 404)
}

func TestUpdateDocument(t *testing.T) {
	tok := adminToken(t)
	id := uploadDoc(t, tok, "update-me-"+uid()+".txt", "before")

	newName := "renamed-" + uid() + ".txt"
	resp := apiDo(t, "PUT", "/documents/"+id,
		map[string]any{"name": newName}, tok)
	mustStatus(t, resp, 200)
	var body map[string]any
	mustDecode(t, resp.Body, &body)
	if body["name"] != newName {
		t.Fatalf("name %v, want %v", body["name"], newName)
	}
}

func TestDeleteDocument_SoftDelete(t *testing.T) {
	tok := adminToken(t)
	id := uploadDoc(t, tok, "delete-me-"+uid()+".txt", "bye")

	resp := apiDo(t, "DELETE", "/documents/"+id, nil, tok)
	mustStatus(t, resp, 200)

	// No longer in active list
	resp2 := apiDo(t, "GET", "/documents", nil, tok)
	mustStatus(t, resp2, 200)
	var body map[string]any
	mustDecode(t, resp2.Body, &body)
	docs, _ := body["documents"].([]any)
	for _, d := range docs {
		dm, _ := d.(map[string]any)
		if dm["id"] == id {
			t.Fatal("soft-deleted document still appears in active list")
		}
	}
}

func TestDeleteDocument_HardDelete(t *testing.T) {
	tok := adminToken(t)
	id := uploadDoc(t, tok, "hard-delete-"+uid()+".txt", "gone")

	resp := apiDo(t, "DELETE", "/documents/"+id+"?hard=true", nil, tok)
	mustStatus(t, resp, 200)

	resp2 := apiDo(t, "GET", "/documents/"+id, nil, tok)
	mustStatus(t, resp2, 404)
}

func TestDownloadDocument(t *testing.T) {
	tok := adminToken(t)
	content := "download content " + uid()
	id := uploadDoc(t, tok, "download-"+uid()+".txt", content)

	resp := apiDo(t, "GET", "/documents/"+id+"/download", nil, tok)
	mustStatus(t, resp, 200)
	if resp.Header.Get("Content-Disposition") == "" {
		t.Fatal("missing Content-Disposition header")
	}
}

func TestSearchDocuments(t *testing.T) {
	tok := adminToken(t)
	marker := "uniquemarkertxt" + uid()
	uploadDoc(t, tok, marker+".txt", "content")

	resp := apiDo(t, "GET", "/documents?search="+marker, nil, tok)
	mustStatus(t, resp, 200)
	var body map[string]any
	mustDecode(t, resp.Body, &body)
	docs, _ := body["documents"].([]any)
	if len(docs) == 0 {
		t.Fatal("search returned no documents for marker " + marker)
	}
}

func TestNewVersion(t *testing.T) {
	tok := adminToken(t)
	id := uploadDoc(t, tok, "versioned-"+uid()+".txt", "v1 content")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "versioned.txt")
	fw.Write([]byte("v2 content"))
	mw.WriteField("comment", "second version")
	mw.Close()

	req, _ := http.NewRequest("POST", testSrv.URL+"/documents/"+id+"/version", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ := http.DefaultClient.Do(req)
	mustStatus(t, resp, 200)

	var body map[string]any
	mustDecode(t, resp.Body, &body)
	if version, _ := body["version"].(float64); version != 2 {
		t.Fatalf("version %v, want 2", body["version"])
	}
}

func TestStats(t *testing.T) {
	tok := adminToken(t)
	resp := apiDo(t, "GET", "/stats", nil, tok)
	mustStatus(t, resp, 200)
	var body map[string]any
	mustDecode(t, resp.Body, &body)
	if _, ok := body["totalDocuments"]; !ok {
		t.Fatal("stats missing totalDocuments")
	}
}
