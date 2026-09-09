package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Personal edits are authoritative user data, deliberately separate from sessions.db.
type webAnnotation struct {
	Title    string `json:"title"`
	Favorite bool   `json:"favorite"`
	Hidden   bool   `json:"hidden"`
}

// Pointer fields distinguish an omitted edit from explicitly clearing a value.
type webAnnotationPatch struct {
	Title    *string `json:"title"`
	Favorite *bool   `json:"favorite"`
	Hidden   *bool   `json:"hidden"`
}
type webLibraryData struct {
	Version     int                      `json:"version"`
	Roots       []string                 `json:"roots"`
	Annotations map[string]webAnnotation `json:"annotations"`
}
type webLibrary struct {
	mu   sync.Mutex
	path string
	data webLibraryData
}

func openWebLibrary(path, home string) (*webLibrary, error) {
	lib := &webLibrary{path: path, data: webLibraryData{Version: 1, Roots: []string{home}, Annotations: map[string]webAnnotation{}}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(b, &lib.data); err != nil {
			return nil, fmt.Errorf("reading personal library: %w", err)
		}
		if lib.data.Version != 1 || lib.data.Annotations == nil {
			return nil, fmt.Errorf("unsupported personal library format")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return lib, nil
}
func (l *webLibrary) snapshot() webLibraryData {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.clone()
}
func (l *webLibrary) clone() webLibraryData {
	d := l.data
	d.Roots = append([]string{}, d.Roots...)
	d.Annotations = make(map[string]webAnnotation, len(l.data.Annotations))
	for k, v := range l.data.Annotations {
		d.Annotations[k] = v
	}
	return d
}
func (l *webLibrary) setAnnotation(id string, a webAnnotation) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.clone()
	d.Annotations[id] = a
	return l.save(d)
}
func (l *webLibrary) patchAnnotation(id string, patch webAnnotationPatch, defaultHidden bool) (webAnnotation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.clone()
	a, exists := d.Annotations[id]
	if !exists {
		a.Hidden = defaultHidden
	}
	if patch.Title != nil {
		a.Title = *patch.Title
	}
	if patch.Favorite != nil {
		a.Favorite = *patch.Favorite
	}
	if patch.Hidden != nil {
		a.Hidden = *patch.Hidden
	}
	d.Annotations[id] = a
	return a, l.save(d)
}
func (l *webLibrary) addRoot(root string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.data.Roots {
		if r == root {
			return nil
		}
	}
	d := l.clone()
	d.Roots = append(d.Roots, root)
	return l.save(d)
}

// Rename keeps interrupted writes from destroying previous personal edits. Commit memory
// only after durable file replacement succeeds; callers can truthfully report save failures.
func (l *webLibrary) save(d webLibraryData) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(l.path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".library-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, l.path); err != nil {
		return err
	}
	l.data = d
	return nil
}
func webWithin(child, parent string) bool {
	if !filepath.IsAbs(child) || !filepath.IsAbs(parent) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
