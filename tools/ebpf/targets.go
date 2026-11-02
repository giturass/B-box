package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func (c collectorConfig) selected(uid uint32) bool {
	if uid == c.ExcludeUID || uid/100000 != c.UserID {
		return false
	}
	if len(c.UIDs) == 0 {
		return true
	}
	for _, selected := range c.UIDs {
		if uid == selected {
			return true
		}
	}
	return false
}

func initialLibraryPaths(config collectorConfig) []string {
	paths := make([]string, 0, len(config.TLSOffsets)+len(config.Libraries)+4)
	for _, value := range config.TLSOffsets {
		paths = append(paths, value.Path)
	}
	paths = append(paths, config.Libraries...)
	return append(paths,
		"/apex/com.android.conscrypt/lib64/libssl.so",
		"/apex/com.android.conscrypt/lib64/libjavacrypto.so",
		"/system/lib64/libssl.so", "/system/lib64/libjavacrypto.so")
}

// PackageManager supplies base/split/shared APKs and nativeLibraryDir. This
// permits attaching before launching the app; /proc supplements dynamically
// loaded libraries and paths visible only inside a process's mount namespace.
func discoverPaths(ctx context.Context, config collectorConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths := make(map[string]bool)
	add := func(path string) {
		if filepath.IsAbs(path) && len(paths) < 4096 {
			paths[path] = true
		}
	}
	for _, path := range initialLibraryPaths(config) {
		add(path)
	}
	targets := append([]string(nil), config.Targets...)
	for path := range config.TargetPackages {
		targets = append(targets, path)
	}
	for _, path := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			add(path)
			continue
		}
		dir, err := os.Open(path)
		if err != nil {
			continue
		}
		entries, _ := dir.ReadDir(4096)
		dir.Close()
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".so") {
				add(filepath.Join(path, entry.Name()))
			}
		}
	}
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("discover process TLS libraries: %w", err)
	}
	for _, process := range processes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pid, err := strconv.ParseUint(process.Name(), 10, 32); err != nil || pid == uint64(os.Getpid()) {
			// The Root collector is a Go executable but is not a target app.
			continue
		}
		directory := filepath.Join("/proc", process.Name())
		info, err := os.Stat(directory)
		if err != nil || !config.selected(info.Sys().(*syscall.Stat_t).Uid) {
			continue
		}
		file, err := os.Open(filepath.Join(directory, "maps"))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(io.LimitReader(file, 8*1024*1024))
		scanner.Buffer(make([]byte, 8192), 64*1024)
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				file.Close()
				return nil, err
			}
			add(mappedLibraryPath(directory, scanner.Text()))
		}
		file.Close()
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Slice(result, func(i, j int) bool {
		priority := func(path string) bool {
			return strings.Contains(path, "libssl") || strings.Contains(path, "libjavacrypto")
		}
		if priority(result[i]) != priority(result[j]) {
			return priority(result[i])
		}
		return result[i] < result[j]
	})
	return result, ctx.Err()
}

// These packages own installed code or share its APK. They are diagnostic
// provenance, not the UID/socket attribution of an observed TLS connection.
func (c collectorConfig) libraryPackages(path string) string {
	names := make(map[string]bool)
	parts := strings.SplitN(path, "/", 5)
	if len(parts) == 5 && parts[1] == "proc" && (parts[3] == "root" || parts[3] == "map_files") {
		if info, err := os.Stat("/proc/" + parts[2]); err == nil {
			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				for _, name := range c.Packages[strconv.FormatUint(uint64(stat.Uid), 10)] {
					names[name] = true
				}
			}
		}
		if parts[3] == "root" {
			path = "/" + parts[4]
		}
	}
	for target, packages := range c.TargetPackages {
		if path == target || strings.HasPrefix(path, strings.TrimSuffix(target, "/")+"/") {
			for _, name := range packages {
				names[name] = true
			}
		}
	}
	values := make([]string, 0, len(names))
	for name := range names {
		values = append(values, name)
	}
	sort.Strings(values)
	if len(values) == 0 {
		return "packages=unknown"
	}
	return fmt.Sprintf("packages=%.512q", strings.Join(values, ","))
}

func mappedLibraryPath(directory, line string) string {
	fields := strings.Fields(line)
	if len(fields) < 6 || !strings.Contains(fields[1], "x") {
		return ""
	}
	name := strings.Join(fields[5:], " ")
	deleted := strings.HasSuffix(name, " (deleted)")
	name = strings.TrimSuffix(name, " (deleted)")
	// Go clients can be standalone executables with no .so suffix. All
	// candidates still undergo bounded ELF/build-info/function validation.
	if !filepath.IsAbs(name) {
		return ""
	}
	path := directory + "/root" + strings.ReplaceAll(name, `\040`, " ")
	device := strings.Split(fields[3], ":")
	if len(device) != 2 {
		return ""
	}
	major, majorErr := strconv.ParseUint(device[0], 16, 32)
	minor, minorErr := strconv.ParseUint(device[1], 16, 32)
	inode, inodeErr := strconv.ParseUint(fields[4], 10, 64)
	if majorErr != nil || minorErr != nil || inodeErr != nil || inode == 0 {
		return ""
	}
	if !deleted {
		if info, err := os.Stat(path); err == nil {
			stat := info.Sys().(*syscall.Stat_t)
			if stat.Ino == inode && uint64(unix.Major(uint64(stat.Dev))) == major && uint64(unix.Minor(uint64(stat.Dev))) == minor {
				return path
			}
		}
	}
	// A replacement at the same name is not the mapped library. map_files
	// follows the original inode, including deleted APKs still used by an app.
	return filepath.Join(directory, "map_files", fields[0])
}
