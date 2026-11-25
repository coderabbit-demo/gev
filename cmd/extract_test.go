// Copyright 2026 CodeRabbit
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const extractionResponse = `{"success":true,"result":{"model":"clairic-27b","answers":{"invoice_number":{"type":"text","value":"INV-2291","confidence":0.99},"total":{"type":"number","value":1080}},"truncated":false,"usage":{"input_tokens":20,"output_tokens":0,"passes":1}}}`

func TestExtractSimpleAndDetailedFields(t *testing.T) {
	t.Setenv("GEV_TOKEN", "")
	path := filepath.Join(t.TempDir(), "fields.json")
	if err := os.WriteFile(path, []byte(`{"total":{"type":"number","decimals":2,"unit":"USD","instructions":"Include shipping"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("custom endpoint received credentials or missing content type")
		}
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if string(request["model"]) != `"clairic"` || string(request["state"]) != `"Invoice INV-2291"` || request["images"] != nil {
			t.Errorf("invalid request: %v", request)
		}
		var fields map[string]map[string]any
		if err := json.Unmarshal(request["fields"], &fields); err != nil || fields["invoice_number"]["type"] != "text" || fields["total"]["decimals"] != float64(2) {
			t.Errorf("invalid fields: %v (%v)", fields, err)
		}
		_, _ = w.Write([]byte(extractionResponse))
	}
	code, out, _, calls := invoke(t, handler, "", "extract", "--text", "Invoice INV-2291", "--field", "invoice_number=text", "--fields", path, "--json")
	if code != 0 || calls != 1 {
		t.Fatalf("code=%d calls=%d output=%s", code, calls, out)
	}
	var result map[string]any
	if json.Unmarshal([]byte(out), &result) != nil || result["command"] != "extract" || result["model"] != "clairic-27b" {
		t.Fatalf("invalid output: %s", out)
	}
	answers := result["answers"].(map[string]any)
	if answers["invoice_number"].(map[string]any)["confidence"] != .99 {
		t.Fatalf("missing answer details: %s", out)
	}
	code, out, _, calls = invoke(t, handler, "", "extract", "--text", "Invoice INV-2291", "--field", "invoice_number=text", "--fields", path)
	if code != 0 || calls != 1 || out != "invoice_number=\"INV-2291\" confidence=0.990\ntotal=1080 confidence=n/a\n" {
		t.Fatalf("human code=%d calls=%d output=%q", code, calls, out)
	}
}

func TestExtractHumanFormatsNullListsAndEscapedText(t *testing.T) {
	t.Setenv("GEV_TOKEN", "")
	response := answer(`{"model":"clairic-27b","answers":{"missing":{"type":"text","value":null,"confidence":0.99},"items":{"type":"list","value":["bread","eggs"],"confidence":0.81},"note":{"type":"text","value":"first\nsecond"}},"truncated":false,"usage":{"input_tokens":3,"output_tokens":0}}`)
	code, out, _, calls := invoke(t, response, "receipt", "extract", "--field", "missing=text", "--field", "items=list:text", "--field", "note=text")
	want := "items=[\"bread\",\"eggs\"] confidence=0.810\nmissing=null confidence=0.990\nnote=\"first\\nsecond\" confidence=n/a\n"
	if code != 0 || calls != 1 || out != want {
		t.Fatalf("code=%d calls=%d output=%q want=%q", code, calls, out, want)
	}
}

func TestExtractImagesAndLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	data := []byte("\x89PNG\r\n\x1a\nmock")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			State  string   `json:"state"`
			Images []string `json:"images"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.State != "" || len(request.Images) != 1 || request.Images[0] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data) {
			t.Errorf("invalid image request: %+v", request)
		}
		_, _ = w.Write([]byte(`{"model":"clairic-27b","answers":{"photo":{"type":"text","value":"image"}},"truncated":false,"usage":{"input_tokens":5,"output_tokens":0}}`))
	}
	code, _, _, calls := invoke(t, handler, "", "extract", "--field", "photo=text", "--image", path)
	if code != 0 || calls != 1 {
		t.Fatalf("code=%d calls=%d", code, calls)
	}
	args := []string{"extract", "--field", "photo=text"}
	for range 5 {
		args = append(args, "--image", path)
	}
	code, _, _, calls = invoke(t, handler, "", args...)
	if code != 2 || calls != 0 {
		t.Fatalf("image count: code=%d calls=%d", code, calls)
	}
}

func TestExtractDocumentWithoutText(t *testing.T) {
	t.Setenv("GEV_TOKEN", "")
	first := []byte("%PDF-1.4\nfirst document\n%%EOF\n")
	second := []byte("%PDF-1.4\nsecond document\n%%EOF\n")
	paths := []string{filepath.Join(t.TempDir(), "first.pdf"), filepath.Join(t.TempDir(), "second.pdf")}
	for i, path := range paths {
		data := first
		if i == 1 {
			data = second
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			State     string   `json:"state"`
			Documents []string `json:"documents"`
			Images    []string `json:"images"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.State != "" || request.Images != nil || len(request.Documents) != 2 || request.Documents[0] != "data:application/pdf;base64,"+base64.StdEncoding.EncodeToString(first) || request.Documents[1] != "data:application/pdf;base64,"+base64.StdEncoding.EncodeToString(second) {
			t.Errorf("invalid document request: %+v", request)
		}
		_, _ = w.Write([]byte(`{"model":"clairic-27b","answers":{"total":{"type":"number","value":10}},"truncated":false,"usage":{"input_tokens":5,"output_tokens":0}}`))
	}
	code, _, _, calls := invoke(t, handler, "", "extract", "--field", "total=number", "--document", paths[0], "--document", paths[1])
	if code != 0 || calls != 1 {
		t.Fatalf("document request: code=%d calls=%d", code, calls)
	}
}

func TestExtractDocumentWithImageAndText(t *testing.T) {
	pdf := filepath.Join(t.TempDir(), "invoice.pdf")
	image := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4\n%%EOF\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("\x89PNG\r\n\x1a\nmock"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			State     string   `json:"state"`
			Documents []string `json:"documents"`
			Images    []string `json:"images"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.State != "extra context" || len(request.Images) != 1 || len(request.Documents) != 1 {
			t.Errorf("invalid mixed request: %+v", request)
		}
		_, _ = w.Write([]byte(`{"model":"clairic-27b","answers":{"total":{"type":"number","value":10}},"truncated":false,"usage":{"input_tokens":5,"output_tokens":0}}`))
	}
	code, _, _, calls := invoke(t, handler, "", "extract", "--text", "extra context", "--image", image, "--document", pdf, "--field", "total=number")
	if code != 0 || calls != 1 {
		t.Fatalf("mixed request: code=%d calls=%d", code, calls)
	}
}

func TestExtractRejectsInvalidDocumentsBeforeProvider(t *testing.T) {
	base := t.TempDir()
	invalid := filepath.Join(base, "invalid.pdf")
	empty := filepath.Join(base, "empty.pdf")
	oversized := filepath.Join(base, "oversized.pdf")
	valid := filepath.Join(base, "valid.pdf")
	for path, data := range map[string][]byte{
		invalid:   []byte("not a PDF"),
		empty:     {},
		oversized: append([]byte("%PDF-1.4\n"), make([]byte, 4<<20)...),
		valid:     []byte("%PDF-1.4\n%%EOF\n"),
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"--document", ""},
		{"--document", filepath.Join(base, "missing.pdf")},
		{"--document", invalid},
		{"--document", empty},
		{"--document", oversized},
		{"--document", valid, "--document", valid, "--document", valid, "--document", valid, "--document", valid},
	} {
		command := append([]string{"extract", "--field", "total=number"}, args...)
		code, _, _, calls := invoke(t, func(http.ResponseWriter, *http.Request) { t.Error("provider called") }, "", command...)
		if code != 2 || calls != 0 {
			t.Errorf("args=%v code=%d calls=%d", args, code, calls)
		}
	}
	image := filepath.Join(base, "photo.png")
	if err := os.WriteFile(image, []byte("\x89PNG\r\n\x1a\nmock"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"extract", "--field", "total=number", "--image", image}
	for range 4 {
		args = append(args, "--document", valid)
	}
	code, _, _, calls := invoke(t, func(http.ResponseWriter, *http.Request) { t.Error("provider called") }, "", args...)
	if code != 2 || calls != 0 {
		t.Fatalf("image/document count: code=%d calls=%d", code, calls)
	}
}

func TestExtractSharedMediaByteLimit(t *testing.T) {
	base := t.TempDir()
	pdf := filepath.Join(base, "large.pdf")
	image := filepath.Join(base, "photo.png")
	data := make([]byte, 4<<20)
	copy(data, "%PDF-1.4\n")
	if err := os.WriteFile(pdf, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("\x89PNG\r\n\x1a\nmock"), 0600); err != nil {
		t.Fatal(err)
	}
	code, _, _, calls := invoke(t, func(http.ResponseWriter, *http.Request) { t.Error("provider called") }, "", "extract", "--field", "total=number", "--image", image, "--document", pdf, "--document", pdf)
	if code != 2 || calls != 0 {
		t.Fatalf("shared media byte limit: code=%d calls=%d", code, calls)
	}
}

func TestExtractInvalidFieldsDoNotCallProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fields.json")
	if err := os.WriteFile(path, []byte(`{"name":{"type":"text"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"extract", "--text", "data"},
		{"extract", "--text", "data", "--field", "bad"},
		{"extract", "--text", "data", "--field", "name=text", "--field", "name=number"},
		{"extract", "--text", "data", "--field", "name=text", "--fields", path},
		{"extract", "--text", "data", "--field", "x=list"},
		{"extract", "--text", "data", "--field", "name=text,instructions="},
		{"extract", "--text", "data", "--field", "name=text,instructions=   "},
		{"extract", "--text", "data", "--field", "name=instructions=the user's name,type=text"},
		{"extract", "--text", "data", "--field", "name=text,unit=USD"},
		{"extract", "--text", "data", "--field", "x=text", "--model", "jev-latest"},
	} {
		code, _, _, calls := invoke(t, func(http.ResponseWriter, *http.Request) { t.Error("provider called") }, "", args...)
		if code != 2 || calls != 0 {
			t.Errorf("args=%v code=%d calls=%d", args, code, calls)
		}
	}
}

func TestExtractFieldInstructions(t *testing.T) {
	t.Setenv("GEV_TOKEN", "")
	handler := func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Fields map[string]struct {
				Type         string `json:"type"`
				Instructions string `json:"instructions"`
			} `json:"fields"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Fields) != 2 || request.Fields["name"].Type != "text" || request.Fields["name"].Instructions != "the user's name, last = first" || request.Fields["total"].Type != "number" || request.Fields["total"].Instructions != "" {
			t.Errorf("unexpected field specs: %+v", request.Fields)
		}
		_, _ = w.Write([]byte(`{"model":"clairic-27b","answers":{"name":{"type":"text","value":"Dana Ruiz"},"total":{"type":"number","value":12}},"truncated":false,"usage":{"input_tokens":3,"output_tokens":0}}`))
	}
	code, _, _, calls := invoke(t, handler, "data", "extract", "--field", "name=text,instructions=the user's name, last = first", "--field", "total=number", "--json")
	if code != 0 || calls != 1 {
		t.Fatalf("code=%d calls=%d", code, calls)
	}
}

func TestInlineFieldOptions(t *testing.T) {
	for _, tc := range []struct {
		input, want string
	}{
		{`name=text,minLength=2,maxLength=50,nullable=false,instructions=the user's name, as printed`, `{"type":"text","minLength":2,"maxLength":50,"nullable":false,"instructions":"the user's name, as printed"}`},
		{`total=number,unit=USD,decimals=2,minimum=0,instructions=total = items + tax`, `{"type":"number","unit":"USD","decimals":2,"minimum":0,"instructions":"total = items + tax"}`},
		{`bill-to=text`, `{"type":"text"}`},
		{`items=list:text,max_items=20,instructions=the list of items purchased`, `{"type":"list","items":"text","max_items":20,"instructions":"the list of items purchased"}`},
		{`prices=list:number,unit=USD,integer=false,decimals=2,source_only=true`, `{"type":"list","items":"number","unit":"USD","integer":false,"decimals":2,"source_only":true}`},
		{`dates=list:date,source_only=true`, `{"type":"list","items":"date","source_only":true}`},
		{`term=duration,unit=days,integer=true,null_threshold=0.6`, `{"type":"duration","unit":"days","integer":true,"null_threshold":0.6}`},
		{`answer=boolean,nullable=false`, `{"type":"boolean","nullable":false}`},
		{`date=date,source_only=true`, `{"type":"date","source_only":true}`},
		{`subtotal=compute,unit=USD,decimals=2`, `{"type":"compute","unit":"USD","decimals":2}`},
	} {
		name, raw, err := parseInlineField(tc.input)
		if err != nil || name == "" {
			t.Errorf("parse %q: %v", tc.input, err)
			continue
		}
		var got, want map[string]any
		if json.Unmarshal(raw, &got) != nil || json.Unmarshal([]byte(tc.want), &want) != nil || !mapsEqual(got, want) {
			t.Errorf("parse %q: got %s; want %s", tc.input, raw, tc.want)
		}
	}
}

func mapsEqual(a, b map[string]any) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	return errLeft == nil && errRight == nil && string(left) == string(right)
}

func TestInlineFieldOptionsRejectInvalidValues(t *testing.T) {
	for _, input := range []string{
		"items=list", "items=list:datetime", "items=list:object", "items=list:",
		"items=list:text,unit=USD", "items=list:date,knowledge=true", "items=list:number,nullable=false",
		"total=number,decimals=7", "total=number,minimum=10,maximum=1",
		"total=number,integer=yes", "total=number,unit=USD,unit=EUR",
		"name=text,minLength=-1", "name=text,minLength=10,maxLength=2",
		"name=text,source_only=true", "name=text,pattern=abc",
		"total=number,knowledge=true,source_only=true",
		"date=date,source_only=true,null_threshold=0.5",
		"date=date,null_threshold=0", "date=date,null_threshold=1.1",
		"term=duration,unit=USD", "items=list:text,max_items=-1",
		"items=list:text,instructions=  ",
	} {
		if _, _, err := parseInlineField(input); err == nil {
			t.Errorf("accepted invalid --field %q", input)
		}
	}
}

func TestExtractInlineListAndNumberRequest(t *testing.T) {
	t.Setenv("GEV_TOKEN", "")
	handler := func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Fields map[string]map[string]any `json:"fields"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Fields["items"]["type"] != "list" || request.Fields["items"]["items"] != "text" || request.Fields["items"]["instructions"] != "the list of items purchased" || request.Fields["total"]["unit"] != "USD" || request.Fields["total"]["decimals"] != float64(2) {
			t.Errorf("unexpected fields: %+v", request.Fields)
		}
		_, _ = w.Write([]byte(`{"model":"clairic-27b","answers":{"items":{"type":"list","value":["bread"]},"total":{"type":"number","value":5.25}},"truncated":false,"usage":{"input_tokens":3,"output_tokens":0}}`))
	}
	code, _, _, calls := invoke(t, handler, "receipt", "extract", "--field", "items=list:text,instructions=the list of items purchased", "--field", "total=number,unit=USD,decimals=2", "--json")
	if code != 0 || calls != 1 {
		t.Fatalf("code=%d calls=%d", code, calls)
	}
}

func TestExtractCustomEndpointUsesGEVToken(t *testing.T) {
	t.Setenv("GEV_TOKEN", "shared-token")
	t.Setenv("CLOUDFLARE_AUTH_TOKEN", "legacy-token")
	handler := func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer shared-token" {
			t.Errorf("unexpected authorization header: %q", got)
		}
		_, _ = w.Write([]byte(`{"model":"clairic-27b","answers":{"name":{"type":"text","value":null}},"truncated":false,"usage":{"input_tokens":3,"output_tokens":0}}`))
	}
	code, _, _, calls := invoke(t, handler, "data", "extract", "--field", "name=text")
	if code != 0 || calls != 1 {
		t.Fatalf("code=%d calls=%d", code, calls)
	}
}

func TestExtractTruncationAndOutputPolicy(t *testing.T) {
	policy := filepath.Join(t.TempDir(), "output.rego")
	source := `package gev.output
import rego.v1
result := {"command": input.command, "answers": {"safe": input.data.answers.safe}}`
	if err := os.WriteFile(policy, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	handler := answer(`{"model":"clairic-27b","answers":{"safe":{"type":"text","value":null},"secret":{"type":"text","value":"hidden"}},"truncated":true,"usage":{"input_tokens":2,"output_tokens":1}}`)
	code, out, _, calls := invoke(t, handler, "data", "extract", "--field", "safe=text", "--field", "secret=text", "--output-policy", policy, "--json")
	if code != 3 || calls != 1 || !strings.Contains(out, `"safe"`) || strings.Contains(out, "hidden") {
		t.Fatalf("code=%d calls=%d output=%s", code, calls, out)
	}
	code, out, _, calls = invoke(t, handler, "data", "extract", "--field", "safe=text", "--field", "secret=text", "--output-policy", policy)
	if code != 3 || calls != 1 || out != "safe=null confidence=n/a\n" {
		t.Fatalf("human policy code=%d calls=%d output=%q", code, calls, out)
	}
}

func TestExtractHostedEndpointRequiresCredentials(t *testing.T) {
	t.Setenv("GEV_API_URL", "")
	t.Setenv("GEV_CLOUDFLARE_ACCOUNT_ID", "")
	t.Setenv("GEV_TOKEN", "")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "")
	root := newRoot()
	root.SetIn(strings.NewReader("data"))
	root.SetArgs([]string{"extract", "--field", "name=text"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "CLOUDFLARE_ACCOUNT_ID") {
		t.Fatalf("expected missing account error; got %v", err)
	}
	t.Setenv("GEV_CLOUDFLARE_ACCOUNT_ID", "test-account")
	t.Setenv("CLOUDFLARE_AUTH_TOKEN", "legacy-token")
	root = newRoot()
	root.SetIn(strings.NewReader("data"))
	root.SetArgs([]string{"extract", "--field", "name=text"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "GEV_TOKEN") {
		t.Fatalf("expected missing token error; got %v", err)
	}
}

func TestCloudflareAccountUsesViperPrefix(t *testing.T) {
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "legacy-account")
	t.Setenv("GEV_CLOUDFLARE_ACCOUNT_ID", "gev-account")
	v := commandConfig(newRoot())
	if v.GetString("cloudflare-account-id") != "gev-account" {
		t.Fatal("Viper did not read the GEV account ID")
	}
	t.Setenv("GEV_CLOUDFLARE_ACCOUNT_ID", "")
	if v.GetString("cloudflare-account-id") != "" {
		t.Fatal("unprefixed Cloudflare account ID must not be used")
	}
}

func TestInvoiceExtractExample(t *testing.T) {
	t.Setenv("GEV_TOKEN", "")
	handler := func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			State  string                     `json:"state"`
			Fields map[string]json.RawMessage `json:"fields"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(request.State, "Invoice INV-2291") || !strings.Contains(string(request.Fields["total"]), "shipping") || len(request.Fields) != 6 {
			t.Errorf("example request did not include the invoice and fields: %+v", request)
		}
		_, _ = w.Write([]byte(`{"model":"clairic-27b","answers":{"invoice_number":{"type":"text","value":"INV-2291"},"due_date":{"type":"date","value":"2026-10-02"},"total":{"type":"number","value":1080},"contacts":{"type":"list","value":["Dana Ruiz","Priya Shah"]},"po_number":{"type":"text","value":null},"vendor":{"type":"text","value":"Northwind Supply"}},"truncated":false,"usage":{"input_tokens":10,"output_tokens":0}}`))
	}
	code, out, _, calls := invoke(t, handler, "", "extract", "--file", "../examples/invoice.txt", "--fields", "../examples/invoice-fields.json", "--field", "vendor=text", "--output-policy", "../examples/extract-output.rego", "--json")
	if code != 0 || calls != 1 || !strings.Contains(out, `"invoice_number"`) || strings.Contains(out, `"contacts"`) || strings.Contains(out, "Dana Ruiz") {
		t.Fatalf("code=%d calls=%d output=%s", code, calls, out)
	}
	code, out, _, calls = invoke(t, handler, "", "extract", "--file", "../examples/invoice.txt", "--fields", "../examples/invoice-fields.json", "--field", "vendor=text", "--output-policy", "../examples/extract-output.rego")
	if code != 0 || calls != 1 || strings.Contains(out, "contacts=") || !strings.Contains(out, "invoice_number=\"INV-2291\" confidence=n/a") {
		t.Fatalf("human policy code=%d calls=%d output=%q", code, calls, out)
	}
}
