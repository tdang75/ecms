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
)

// Candidate URLs tried in order; first successful download wins.
var samplePDFURLs = []string{
	"https://www.irs.gov/pub/irs-pdf/fw9.pdf",                          // IRS W-9 form
	"https://www.w3.org/WAI/WCAG21/Techniques/pdf/pdf-sample.pdf",      // W3C sample
	"https://mozilla.github.io/pdf.js/web/compressed.tracemonkey-pldi-09.pdf", // Mozilla PDF.js test
}

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
	// 1. Download a sample PDF; fall back to a generated one if all URLs fail.
	pdfBytes, source := fetchFirstWorking(samplePDFURLs)
	if pdfBytes == nil {
		fmt.Println("All remote URLs failed — using generated sample PDF")
		pdfBytes = generateSamplePDF()
		source = "generated locally"
	}
	fmt.Printf("Sample document ready (%d bytes, source: %s)\n", len(pdfBytes), source)

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

// ── PDF fetch / generate ──────────────────────────────────────────────────────

// fetchFirstWorking tries each URL in order and returns the first successful
// response body along with the URL that worked.
func fetchFirstWorking(urls []string) ([]byte, string) {
	for _, u := range urls {
		fmt.Printf("Trying %s … ", u)
		data, err := fetchURL(u)
		if err != nil {
			fmt.Printf("failed (%v)\n", err)
			continue
		}
		fmt.Printf("OK\n")
		return data, u
	}
	return nil, ""
}

// generateSamplePDF builds a minimal but valid single-page PDF in memory.
// It uses correct xref byte offsets so any conforming reader can open it.
func generateSamplePDF() []byte {
	lines := []string{
		"BT",
		"/F1 14 Tf",
		"72 720 Td",
		"(Sample Contract Document) Tj",
		"0 -24 Td (Counterparty: Acme Corporation) Tj",
		"0 -24 Td (Contract Value: USD 75,000.00) Tj",
		"0 -24 Td (Effective Date: 2026-01-01) Tj",
		"0 -24 Td (Expiry Date: 2027-12-31) Tj",
		"ET",
	}
	stream := ""
	for _, l := range lines {
		stream += l + "\n"
	}

	var buf bytes.Buffer
	offsets := make([]int, 6)

	buf.WriteString("%PDF-1.4\n")
	offsets[1] = buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = buf.Len()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	offsets[3] = buf.Len()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792]\n" +
		"   /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")
	offsets[4] = buf.Len()
	buf.WriteString(fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(stream), stream))
	offsets[5] = buf.Len()
	buf.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	xrefPos := buf.Len()
	buf.WriteString("xref\n0 6\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		buf.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}
	buf.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\n")
	buf.WriteString(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefPos))

	return buf.Bytes()
}

// ── API helpers ───────────────────────────────────────────────────────────────

func fetchURL(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

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

func uploadDocument(token string, data []byte, filename string, p uploadParams) (map[string]any, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err = fw.Write(data); err != nil {
		return nil, err
	}

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
