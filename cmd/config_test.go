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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func executeConfigured(t *testing.T, args ...string) (error, string) {
	t.Helper()
	root := newRoot()
	root.SetIn(strings.NewReader("data"))
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs(args)
	return root.Execute(), output.String()
}

func TestDefaultHomeConfigFormats(t *testing.T) {
	for _, format := range []struct {
		extension string
		content   string
	}{
		{"json", `{"api-url":%q,"model":"config-model","token":"config-token"}`},
		{"yaml", "api-url: %s\nmodel: config-model\ntoken: config-token\n"},
		{"toml", "api-url = %q\nmodel = \"config-model\"\ntoken = \"config-token\"\n"},
	} {
		t.Run(format.extension, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("GEV_CONFIG", "")
			t.Setenv("GEV_API_URL", "")
			t.Setenv("GEV_MODEL", "")
			t.Setenv("GEV_TOKEN", "")
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer config-token" {
					t.Errorf("config token not sent: %q", r.Header.Get("Authorization"))
				}
				var body struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "config-model" {
					t.Errorf("config model not used: %+v (%v)", body, err)
				}
				_, _ = w.Write([]byte(`{"answers":{"q":{"noul":0.9}}}`))
			}))
			defer server.Close()
			path := filepath.Join(home, ".gev."+format.extension)
			if err := os.WriteFile(path, []byte(fmt.Sprintf(format.content, server.URL)), 0600); err != nil {
				t.Fatal(err)
			}
			err, output := executeConfigured(t, "eval", "--question", "q=example")
			if err != nil || calls != 1 || !strings.Contains(output, "q TRUE") {
				t.Fatalf("default %s config: calls=%d output=%q err=%v", format.extension, calls, output, err)
			}
		})
	}
}

func TestExplicitConfigOverridesHomeSearch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GEV_CONFIG", "")
	t.Setenv("GEV_API_URL", "")
	t.Setenv("GEV_MODEL", "")
	t.Setenv("GEV_TOKEN", "")
	if err := os.WriteFile(filepath.Join(home, ".gev.json"), []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"q":{"noul":0.9}}}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "other.yaml")
	if err := os.WriteFile(path, []byte("api-url: "+server.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, useEnv := range []bool{false, true} {
		args := []string{"eval", "--question", "q=example"}
		if useEnv {
			t.Setenv("GEV_CONFIG", path)
		} else {
			args = append(args, "--config", path)
		}
		err, output := executeConfigured(t, args...)
		if err != nil || !strings.Contains(output, "q TRUE") {
			t.Fatalf("explicit config via env=%t: output=%q err=%v", useEnv, output, err)
		}
	}
}

func TestHomeConfigPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GEV_CONFIG", "")
	t.Setenv("GEV_TOKEN", "")
	t.Setenv("GEV_API_URL", "")
	t.Setenv("GEV_MODEL", "env-model")
	var models []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		models = append(models, body.Model)
		_, _ = w.Write([]byte(`{"answers":{"q":{"noul":0.9}}}`))
	}))
	defer server.Close()
	path := filepath.Join(home, ".gev.json")
	data := fmt.Sprintf(`{"api-url":%q,"model":"config-model"}`, server.URL)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err, _ := executeConfigured(t, "eval", "--question", "q=example"); err != nil {
		t.Fatal(err)
	}
	if err, _ := executeConfigured(t, "eval", "--question", "q=example", "--model", "flag-model"); err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "env-model" || models[1] != "flag-model" {
		t.Fatalf("config precedence: %v", models)
	}
}

func TestDefaultConfigErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GEV_CONFIG", "")
	err, _ := executeConfigured(t, "eval", "--question", "q=example", "--text", "")
	if err == nil || !strings.Contains(err.Error(), "input is empty") {
		t.Fatalf("missing default config must not fail: %v", err)
	}
	for _, file := range []string{filepath.Join(home, ".gev.json"), filepath.Join(home, ".gev.yaml"), filepath.Join(home, ".gev.toml")} {
		if err := os.WriteFile(file, []byte("invalid data !"), 0600); err != nil {
			t.Fatal(err)
		}
		got, _ := executeConfigured(t, "eval", "--question", "q=example")
		var status *exitError
		if !errors.As(got, &status) || status.code != 2 || !strings.Contains(status.message, "config") {
			t.Fatalf("bad default config %q: %v", file, got)
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := executeConfigured(t, "eval", "--question", "q=example", "--config", filepath.Join(home, "missing.json"))
	var status *exitError
	if !errors.As(got, &status) || status.code != 2 || !strings.Contains(status.message, "config") {
		t.Fatalf("missing explicit config: %v", got)
	}
}
