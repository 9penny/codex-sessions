package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

func TestEnrichLiveRequiresExplicitConfirmation(t *testing.T) {
	command := CreateEnrichCommand()
	command.SetArgs(nil)
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "requires --yes") {
		t.Fatalf("Execute() error = %v; want explicit confirmation error", err)
	}
}

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

func TestResolveEnrichmentScope(t *testing.T) {
	current := t.TempDir()
	t.Chdir(current)
	wantCurrentID, _, err := utils.ComputeProjectID(current)
	if err != nil {
		t.Fatal(err)
	}

	currentScope, err := resolveEnrichmentScope(false, "")
	if err != nil || currentScope.ProjectID != wantCurrentID || !strings.Contains(currentScope.Label, "current project") {
		t.Fatalf("current scope = %+v, %v", currentScope, err)
	}

	explicit := filepath.Join(t.TempDir(), "chosen-project")
	if err := os.Mkdir(explicit, 0o755); err != nil {
		t.Fatal(err)
	}
	wantExplicitID, _, err := utils.ComputeProjectID(explicit)
	if err != nil {
		t.Fatal(err)
	}
	explicitScope, err := resolveEnrichmentScope(false, explicit)
	if err != nil || explicitScope.ProjectID != wantExplicitID || !strings.Contains(explicitScope.Label, "project") {
		t.Fatalf("explicit scope = %+v, %v", explicitScope, err)
	}

	allScope, err := resolveEnrichmentScope(true, "")
	if err != nil || allScope.ProjectID != "" || allScope.Label != "all projects" {
		t.Fatalf("all scope = %+v, %v", allScope, err)
	}
}

func TestResolveEnrichmentScopeRejectsConflictingFlags(t *testing.T) {
	_, err := resolveEnrichmentScope(true, ".")
	if err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("error = %v; want conflicting scope flags", err)
	}
}

func TestEnrichCommandDefaultsToCurrentProjectAndRequiresAllForGlobal(t *testing.T) {
	dataHome := filepath.Join(t.TempDir(), "data")
	project := filepath.Join(t.TempDir(), "current-project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Chdir(project)
	projectID, _, err := utils.ComputeProjectID(project)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, err := sessionindex.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	store, err := sessionindex.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	target := indexedEnrichmentFixture(t, store)
	target.ProjectID = projectID
	target.ProjectName = "current-project"
	target.SessionID = "33333333-3333-3333-3333-333333333333"
	if err := store.Upsert(target); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	runDry := func(args ...string) string {
		t.Helper()
		command := CreateEnrichCommand()
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetArgs(append([]string{"--dry-run"}, args...))
		if err := command.Execute(); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	if got := runDry(); !strings.Contains(got, "Scope: current project") || !strings.Contains(got, "candidates=1") {
		t.Fatalf("default output = %q; want one current-project candidate", got)
	}
	if got := runDry("--project", project); !strings.Contains(got, "Scope: project") || !strings.Contains(got, "candidates=1") {
		t.Fatalf("project output = %q; want one explicit-project candidate", got)
	}
	if got := runDry("--all"); !strings.Contains(got, "Scope: all projects") || !strings.Contains(got, "candidates=2") {
		t.Fatalf("all output = %q; want both global candidates", got)
	}
}
