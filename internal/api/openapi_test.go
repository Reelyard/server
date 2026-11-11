package api

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every operation in openapi.yaml must exist on the server.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	e := newClientsServer(t)
	resp, body := get(t, e.srv.URL+"/api/v1/openapi.yaml")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/yaml") {
		t.Fatalf("openapi.yaml: %d", resp.StatusCode)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Paths) < 15 {
		t.Fatalf("only %d paths", len(doc.Paths))
	}
	param := regexp.MustCompile(`\{[^}]+\}`)
	for path, ops := range doc.Paths {
		for method := range ops {
			m := strings.ToUpper(method)
			url := e.srv.URL + "/api/v1" + param.ReplaceAllString(path, "tt1")
			if strings.Contains(path, "{id}") {
				url = e.srv.URL + "/api/v1" + param.ReplaceAllString(path, "1")
			}
			req, _ := http.NewRequest(m, url, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusMethodNotAllowed || strings.Contains(string(b), "no such API endpoint") {
				t.Errorf("%s %s is documented but not served (%d)", m, path, resp.StatusCode)
			}
		}
	}
}
