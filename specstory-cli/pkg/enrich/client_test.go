package enrich

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
)

func TestListModelsAuthenticatesAndSortsIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "z-model"}, {"id": "a-model"}}})
	}))
	defer server.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(models, []string{"a-model", "z-model"}) {
		t.Fatalf("models = %v", models)
	}
}

func TestClientRejectsHTTPSDowngradeRedirect(t *testing.T) {
	client, err := NewClient(Config{APIKey: "test-key", BaseURL: "https://api.example.test/v1"})
	if err != nil {
		t.Fatal(err)
	}
	redirected := &http.Request{URL: &url.URL{Scheme: "http", Host: "api.example.test", Path: "/v1/models"}}
	via := []*http.Request{{URL: &url.URL{Scheme: "https", Host: "api.example.test", Path: "/v1/models"}}}
	if err := client.http.CheckRedirect(redirected, via); err == nil {
		t.Fatal("HTTPS-to-HTTP redirect unexpectedly allowed")
	}
}

func TestListModelsRejectsCrossHostRedirectWithoutForwardingKey(t *testing.T) {
	received := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		if r.Header.Get("Authorization") != "" {
			t.Error("redirect target received Authorization")
		}
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()

	client, err := NewClient(Config{APIKey: "redirect-secret", BaseURL: source.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListModels(context.Background()); err == nil {
		t.Fatal("ListModels() unexpectedly followed cross-host redirect")
	}
	if received {
		t.Fatal("cross-host redirect target was contacted")
	}
}
