package httpdebug

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenStoreRemovesRetiredJournalsAndKeepsRequests(t *testing.T) {
	for _, name := range []string{"only retired files", "unrelated files"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			store, err := OpenStore(directory)
			if err != nil {
				t.Fatal(err)
			}
			retired := filepath.Join(directory, "wire")
			if _, err := os.Lstat(retired); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("new capture stores must not create retired storage", err)
			}
			request, err := http.NewRequest("GET", "https://api.test/kept", nil)
			if err != nil {
				t.Fatal(err)
			}
			id := store.begin(Metadata{CaptureSource: "eBPF"}, request, 64)
			store.finish(id)
			if err := os.Mkdir(retired, 0o700); err != nil {
				t.Fatal(err)
			}
			owned := []string{strings.Repeat("a1", 32) + ".wire", "warning.txt"}
			for _, file := range owned {
				if err := os.WriteFile(filepath.Join(retired, file), []byte("retired fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var kept []string
			if name == "unrelated files" {
				subdirectory := filepath.Join(retired, strings.Repeat("b2", 32)+".wire")
				if err := os.Mkdir(subdirectory, 0o700); err != nil {
					t.Fatal(err)
				}
				kept = []string{filepath.Join(retired, "notes.txt"), filepath.Join(retired, "unrelated.wire"), filepath.Join(subdirectory, "kept")}
				for _, file := range kept {
					if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			for range 2 {
				reopened, err := OpenStore(directory)
				if err != nil {
					t.Fatal(err)
				}
				if reopened.Page(1, "").Total != 1 {
					t.Fatal("migration removed retained requests")
				}
				if command, err := reopened.Curl(id); err != nil || !strings.Contains(command, request.URL.String()) {
					t.Fatal("retained request can no longer be exported", err)
				}
				for _, file := range owned {
					if _, err := os.Lstat(filepath.Join(retired, file)); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("retired capture data remains", file, err)
					}
				}
				for _, file := range kept {
					if data, err := os.ReadFile(file); err != nil || string(data) != "keep" {
						t.Fatal("migration modified unrelated data", file, err)
					}
				}
				if len(kept) == 0 {
					if _, err := os.Lstat(retired); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("empty retired directory remains", err)
					}
				}
			}
		})
	}
}

func TestRetiredCaptureCleanupDoesNotFollowDirectorySymlinks(t *testing.T) {
	directory, other := t.TempDir(), t.TempDir()
	kept := filepath.Join(other, strings.Repeat("ab", 32)+".wire")
	if err := os.WriteFile(kept, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(directory, "wire")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(directory); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(kept); err != nil || string(data) != "keep" {
		t.Fatal("migration followed a directory symlink", err)
	}
}
