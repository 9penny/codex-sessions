package factory

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/copilotide"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestActiveRegistryExposesOnlyCodex(t *testing.T) {
	r := &Registry{providers: make(map[string]spi.Provider)}
	r.registerAll()

	want := []string{"codex"}
	if got := r.ListIDsUnsafe(); !slices.Equal(got, want) {
		t.Fatalf("active provider IDs = %v, want %v", got, want)
	}
	if _, err := r.Get("claude"); err == nil {
		t.Fatal("archived Claude provider remains reachable")
	}
	if provider, err := r.Get("codex"); err != nil || provider == nil {
		t.Fatalf("Codex provider unavailable: provider=%v err=%v", provider, err)
	}
}

// Archived provider storage must not affect the active Codex-only registry.
func TestArchivedVariantIsNotRegisteredViaUserDataDirOverride(t *testing.T) {
	variant := copilotide.VSCodium

	// If the host has a real install with chats, the variant registers with or
	// without the override and the assertion below would prove nothing.
	if copilotide.HasAnyChatSessions(variant) {
		t.Skipf("host has a real %s install with Copilot chats; cannot isolate the override path", variant.AppName)
	}

	// Fake user-data-dir with one workspace holding a chatSessions directory —
	// the marker HasAnyChatSessions gates variant registration on.
	userDataDir := t.TempDir()
	chatSessions := filepath.Join(userDataDir, "User", "workspaceStorage", "ws1", "chatSessions")
	if err := os.MkdirAll(chatSessions, 0755); err != nil {
		t.Fatalf("Failed to create fake chatSessions: %v", err)
	}

	copilotide.SetUserDataDirOverride(variant.ID, userDataDir)
	t.Cleanup(func() { copilotide.SetUserDataDirOverride(variant.ID, "") })

	// A fresh Registry rather than the global singleton: the singleton's
	// registration may already have run without the override in other tests.
	r := &Registry{providers: make(map[string]spi.Provider)}
	r.registerAll()

	if _, ok := r.providers[variant.ID]; ok {
		t.Errorf("archived variant %q was registered through a user-data-dir override; registered providers: %v",
			variant.ID, r.ListIDsUnsafe())
	}
}
