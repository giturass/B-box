package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMappedLibraryUsesTheOriginalInodeAfterPackageReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "root", "data", "app", "base.apk")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("mapped inode"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	line := fmt.Sprintf("1000-2000 r-xp 00004000 %x:%x %d /data/app/base.apk",
		unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev)), stat.Ino)
	if got := mappedLibraryPath(directory, line); got != path {
		t.Fatalf("mapped library path=%q", got)
	}
	if got := mappedLibraryPath(directory, strings.Replace(line, "r-xp", "r--p", 1)); got != "" {
		t.Fatal("non-executable mapping selected", got)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("updated package inode"), 0600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(directory, "map_files", "1000-2000")
	for _, value := range []string{line, line + " (deleted)"} {
		if got := mappedLibraryPath(directory, value); got != want {
			t.Fatalf("selected replacement inode: got=%q want=%q", got, want)
		}
	}
}

func TestLibraryPackagesIdentifyInstalledSharedAndProcessCode(t *testing.T) {
	c := collectorConfig{
		TargetPackages: map[string][]string{
			"/product/priv-app/Velvet/Velvet.apk": {"com.google.android.googlequicksearchbox"},
			"/data/app/example/lib/arm64":         {"com.example"},
			"/data/app/shared/base.apk":           {"com.example.two", "com.example.one", "com.example.one"},
		},
		Packages: map[string][]string{fmt.Sprint(os.Getuid()): {"com.process"}},
	}
	for path, expected := range map[string]string{
		"/product/priv-app/Velvet/Velvet.apk":                    `packages="com.google.android.googlequicksearchbox"`,
		"/data/app/example/lib/arm64/libgojni.so":                `packages="com.example"`,
		"/data/app/shared/base.apk":                              `packages="com.example.one,com.example.two"`,
		"/data/app/example/lib/arm64-other/libssl.so":            "packages=unknown",
		"/proc/self/root/data/app/example/lib/arm64/libgojni.so": `packages="com.example,com.process"`,
		"/proc/self/map_files/1000-2000":                         `packages="com.process"`,
	} {
		if got := c.libraryPackages(path); got != expected {
			t.Fatalf("%s: got %s, want %s", path, got, expected)
		}
	}
}
