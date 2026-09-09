package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWebLibraryDurableEditsAndFailedWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	lib, err := openWebLibrary(path, "/home/example")
	if err != nil {
		t.Fatal(err)
	}
	want := webAnnotation{Title: "我的项目", Favorite: true, Hidden: true}
	if err := lib.setAnnotation("session", want); err != nil {
		t.Fatal(err)
	}
	reopened, err := openWebLibrary(path, "/different-home")
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.snapshot().Annotations["session"]; got != want {
		t.Fatalf("annotation lost: %+v", got)
	}
	if got := reopened.snapshot().Roots; len(got) != 1 || got[0] != "/home/example" {
		t.Fatalf("roots lost: %v", got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v", info.Mode())
	}
	// A failed persistence must not silently change the in-memory library.
	lib.path = filepath.Join(path, "not-a-directory")
	if err := lib.setAnnotation("session", webAnnotation{}); err == nil {
		t.Fatal("expected write failure")
	}
	if lib.snapshot().Annotations["session"] != want {
		t.Fatal("failed write changed visible state")
	}
}

func TestWebDirectoryScope(t *testing.T) {
	for _, tc := range []struct {
		child, parent string
		want          bool
	}{
		{"/home/me/Projects/app/sub", "/home/me/Projects", true},
		{"/home/me/Projects-old", "/home/me/Projects", false},
		{"/home/me/Projects", "/home/me/Projects", true},
		{"", "/home/me", false},
	} {
		if got := webWithin(tc.child, tc.parent); got != tc.want {
			t.Errorf("within(%q,%q)=%v", tc.child, tc.parent, got)
		}
	}
}
