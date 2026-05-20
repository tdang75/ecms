// go-api-client demonstrates uploading a document to ECMS via the REST API.
// It downloads a sample PDF from the internet, authenticates, and inserts it
// as a Contract document with pre-filled property values.
//
// Usage:
//
//	go run main.go
//
// Requires ECMS running on localhost:3001 (docker compose up).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
)

// ── Configuration ─────────────────────────────────────────────────────────────

const (
	baseURL  = "http://localhost:3001"
	username = "admin"
	password = "admin"

	// "Contract" document class — seeded by ECMS on first startup.
	contractClassID = "00000000-0000-0000-0000-000000000002"

	// A freely available two-page sample PDF.
	samplePDFURL = "https://www.africau.edu/images/default/sample.pdf"
)

// Property template IDs assigned to the Contract class (seeded at startup).
const (
	propCounterparty  = "10000000-0000-0000-0000-000000000005" // string
	propContractValue = "10000000-0000-0000-0000-000000000004" // decimal
	propEffectiveDate = "10000000-0000-0000-0000-000000000002" // date
	propExpiryDate    = "10000000-0000-0000-0000-000000000003" // date
)

// ── Types ─────────────────────────────────────────────────────────────────────

type propInput struct {
	PropertyTemplateID string `json:"property_template_id"`
	Value              string `json:"value"`
}

type uploadParams struct {
	Name        string
	Description string
	ClassID     string
	Category    string
	Tags        []string
	Properties  []propInput
}

// ── Main ─────────────────────────────────────────────────────────────────────

func main() {
	// 1. Download sample document from the internet.
	fmt.Printf("Downloading sample PDF from %s …\n", samplePDFURL)
	pdfBytes, err := fetchURL(samplePDFURL)
	if err != nil {
		fatalf("download: %v", err)
	}
	fmt.Printf("Downloaded %d bytes\n", len(pdfBytes))

	// 2. Authenticate and obtain a JWT.
	fmt.Println("Authenticating …")
	token, err := login(username, password)
	if err != nil {
		fatalf("login: %v", err)
	}
	fmt.Println("Authenticated ✓")

	// 3. Upload the document with hardcoded Contract class and properties.
	params := uploadParams{
		Name:        "Sample Service Agreement",
		Description: "Auto-uploaded sample contract via Go API client",
		ClassID:     contractClassID,
		Category:    "Legal",
		Tags:        []string{"sample", "contract", "api-client"},
		Properties: []propInput{
			{propCounterparty,  "Acme Corporation"},
			{propContractValue, "75000.00"},
			{propEffectiveDate, "2026-01-01"},
			{propExpiryDate,    "2027-12-31"},
		},
	}

	fmt.Println("Uploading document …")
	doc, err := uploadDocument(token, pdfBytes, "sample-contract.pdf", params)
	if err != nil {
		fatalf("upload: %v", err)
	}

	fmt.Println("Document created ✓")
	fmt.Printf("  ID:      %s\n", str(doc["id"]))
	fmt.Printf("  Name:    %s\n", str(doc["name"]))
	fmt.Printf("  Class:   %s %s\n", str(doc["class_icon"]), str(doc["class_name"]))
	fmt.Printf("  Status:  %s\n", str(doc["status"]))
	fmt.Printf("  Version: v%.0f\n", numF(doc["version"]))
	fmt.Printf("  Size:    %.0f bytes\n", numF(doc["file_size"]))
}

// ── API helpers ───────────────────────────────────────────────────────────────

// fetchURL downloads the content at url and returns it as a byte slice.
func fetchURL(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// login calls POST /auth/login and returns the JWT token.
func login(user, pass string) (string, error) {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := http.Post(baseURL+"/auth/login", "application/json", bytes.NewReader(body)) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	token, ok := result["token"].(string)
	if !ok {
		return "", fmt.Errorf("unexpected response: %v", result)
	}
	return token, nil
}

// uploadDocument posts a multipart form to POST /documents and returns the
// created document JSON.
func uploadDocument(token string, data []byte, filename string, p uploadParams) (map[string]any, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	// Attach the file.
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err = fw.Write(data); err != nil {
		return nil, err
	}

	// Attach metadata fields.
	_ = w.WriteField("name", p.Name)
	_ = w.WriteField("description", p.Description)
	_ = w.WriteField("class_id", p.ClassID)
	_ = w.WriteField("category", p.Category)

	if len(p.Tags) > 0 {
		tagsJSON, _ := json.Marshal(p.Tags)
		_ = w.WriteField("tags", string(tagsJSON))
	}
	if len(p.Properties) > 0 {
		propsJSON, _ := json.Marshal(p.Properties)
		_ = w.WriteField("properties", string(propsJSON))
	}

	w.Close()

	req, err := http.NewRequest(http.MethodPost, baseURL+"/documents", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("HTTP %d: %v", resp.StatusCode, result)
	}
	return result, nil
}

// ── Utility ───────────────────────────────────────────────────────────────────

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func numF(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
