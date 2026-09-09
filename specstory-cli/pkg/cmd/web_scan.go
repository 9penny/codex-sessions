package cmd

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/codexcli"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
)

type webScanStatus struct {
	Running      bool   `json:"running"`
	Directories  int    `json:"directories"`
	Found        int    `json:"found"`
	Indexed      int    `json:"indexed"`
	Skipped      int    `json:"skipped"`
	Errors       int    `json:"errors"`
	LastFinished string `json:"lastFinished"`
	Error        string `json:"error,omitempty"`
}
type webScanner struct {
	mu     sync.Mutex
	runMu  sync.Mutex
	status webScanStatus
	store  *sessionindex.Store
	// Source directories discovered by a full home scan are watched by subsequent passes.
	sources map[string]bool
}

func newWebScanner(store *sessionindex.Store) *webScanner {
	return &webScanner{store: store, sources: map[string]bool{}}
}
func (s *webScanner) snapshot() webScanStatus        { s.mu.Lock(); defer s.mu.Unlock(); return s.status }
func (s *webScanner) change(fn func(*webScanStatus)) { s.mu.Lock(); defer s.mu.Unlock(); fn(&s.status) }
func webSourceRoot(path string) string {
	dir := filepath.Dir(path)
	for p := dir; p != filepath.Dir(p); p = filepath.Dir(p) {
		if filepath.Base(p) == "sessions" {
			return p
		}
	}
	return dir
}

// Full scans discover arbitrary native JSONL beneath the selected roots. Incremental passes
// visit only discovered source directories and the standard store. Never follow symlinks,
// parse dependency fixtures, or infer deletions from incomplete home-directory scans.
func (s *webScanner) run(ctx context.Context, roots []string, full bool) (result error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.change(func(st *webScanStatus) { *st = webScanStatus{Running: true} })
	defer func() {
		s.change(func(st *webScanStatus) {
			st.Running = false
			st.LastFinished = time.Now().Format(time.RFC3339)
			if result != nil {
				st.Error = "扫描未完成，请重试"
			}
		})
	}()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	locations := []string{filepath.Join(codexHome, "sessions")}
	if full {
		locations = append(locations, roots...)
	} else {
		for root := range s.sources {
			locations = append(locations, root)
		}
	}
	fps, err := s.store.Fingerprints()
	if err != nil {
		return err
	}
	provider := codexcli.NewProvider()
	cache := &projectIDCache{m: map[string]projectIDName{}}
	visited := map[string]bool{}
	seenIDs := map[string]bool{}
	indexedAt := time.Now().UTC().Format(time.RFC3339)
	for _, root := range locations {
		// A machine without a default Codex store can still contain custom stores under HOME.
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				s.change(func(st *webScanStatus) { st.Errors++ })
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				if path != root {
					switch d.Name() {
					case ".git", "node_modules", ".cache", ".venv", "venv", "vendor", ".Trash":
						s.change(func(st *webScanStatus) { st.Skipped++ })
						return filepath.SkipDir
					}
				}
				s.change(func(st *webScanStatus) { st.Directories++ })
				return nil
			}
			if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".jsonl") || visited[path] {
				return nil
			}
			visited[path] = true
			info, err := d.Info()
			if err != nil {
				s.change(func(st *webScanStatus) { st.Errors++ })
				return nil
			}
			// Header parsing is bounded by the existing Codex parser. Other JSONL formats are ignored.
			ref, err := provider.InspectSessionFile(path)
			if err != nil || ref == nil || ref.SessionID == "" {
				return nil
			}
			if seenIDs[ref.SessionID] {
				return nil
			}
			seenIDs[ref.SessionID] = true
			s.sources[webSourceRoot(path)] = true
			s.change(func(st *webScanStatus) { st.Found++ })
			fp, exists := fps[sessionindex.FingerprintKey("codex", ref.SessionID)]
			if exists && (fp.Deleted || (fp.NativePath == path && fp.Size == info.Size() && fp.Mtime == info.ModTime().UnixMilli() && fp.Version == reindexVersion)) {
				return nil
			}
			sess := buildSession(reindexItem{agent: "codex", prov: provider, ref: *ref, size: info.Size(), mtime: info.ModTime().UnixMilli(), isNew: !exists}, cache, indexedAt)
			if err := s.store.UpsertBatch([]sessionindex.Session{sess}); err != nil {
				return err
			}
			s.change(func(st *webScanStatus) { st.Indexed++ })
			return nil
		})
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}
