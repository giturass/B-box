package main

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
)

func TestInvalidLibraryPathsIdentifyTheirSource(t *testing.T) {
	for _, group := range []struct{ field, label string }{
		{"libraries", "TLS library"},
		{"targets", "installed capture target"},
	} {
		t.Run(group.field, func(t *testing.T) {
			for _, path := range []string{
				"libOpenCL.so", "/data/app/null\x00.apk", "/data/app/line\nbreak.apk",
				"/data/app/line\rbreak.apk", "/" + strings.Repeat("a", 4096),
				"/" + strings.Repeat("é", 2048),
			} {
				settings, err := json.Marshal(map[string][]string{group.field: {"/data/app/base.apk", path}})
				if err != nil {
					t.Fatal(err)
				}
				_, err = readConfig(bufio.NewReaderSize(strings.NewReader(string(settings)+"\n"), 16384))
				want := "invalid " + group.label + " path at index 1: "
				if err == nil || !strings.HasPrefix(err.Error(), want) {
					t.Fatalf("expected source and index %q, got %v", want, err)
				}
				if strings.ContainsAny(err.Error(), "\x00\r\n") || len(err.Error()) > 4096 {
					t.Fatalf("path diagnostic is not bounded and escaped: %q", err)
				}
				if path == "libOpenCL.so" && !strings.Contains(err.Error(), `"libOpenCL.so"`) {
					t.Fatalf("native soname is missing from diagnostic: %v", err)
				}
			}
		})
	}
}
