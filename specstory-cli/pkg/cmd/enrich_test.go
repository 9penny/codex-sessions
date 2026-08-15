package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnrichModelsIsExplicitAndPrintsOnlyModelIDs(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer command-key" {
			t.Error("missing command API authorization")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "cheap-model"}}})
	}))
	defer server.Close()
	t.Setenv("CSESSIONS_OPENAI_API_KEY", "command-key")
	t.Setenv("CSESSIONS_OPENAI_BASE_URL", server.URL+"/v1")

	command := CreateEnrichCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"models"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if got := strings.TrimSpace(output.String()); got != "cheap-model" {
		t.Fatalf("output = %q", got)
	}
}
