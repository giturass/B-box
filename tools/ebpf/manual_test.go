package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func manualFixture(data []byte, path, entry string) manualTLSOffsets {
	return manualTLSOffsets{Path: path, Entry: entry, SHA256: fmt.Sprintf("%x", sha256.Sum256(data)),
		Functions: map[string]string{"SSL_new": "0x200", "SSL_free": "0x220", "SSL_read": "0x240", "SSL_write": "0x260"}}
}

func TestManualTLSResolvesOnlyFileOffsetsForTheExactELF(t *testing.T) {
	for _, machine := range []elf.Machine{elf.EM_AARCH64, elf.EM_X86_64} {
		data := symbolFixture()
		binary.LittleEndian.PutUint16(data[18:], uint16(machine))
		// Remove the symbol table. Manual input must work without auto discovery.
		binary.LittleEndian.PutUint32(data[0x800+2*64+4:], uint32(elf.SHT_PROGBITS))
		value := manualFixture(data, "/libprivate.so", "")
		symbols, method, err := resolveManualTLS(context.Background(), bytes.NewReader(data), int64(len(data)), value, machine)
		if err != nil || !strings.Contains(method, "ABI supplied by user") || symbols["SSL_read"] != 0x240 {
			t.Fatal(symbols, method, err)
		}
		for _, offset := range []string{"0x5240", "0x100", "0x280", "0xfffffffffffffff0"} {
			value.Functions["SSL_read"] = offset
			if _, _, err := resolveManualTLS(context.Background(), bytes.NewReader(data), int64(len(data)), value, machine); err == nil {
				t.Fatalf("virtual, data or out-of-range offset accepted: %s", offset)
			}
		}
		if machine == elf.EM_AARCH64 {
			value.Functions["SSL_read"] = "0x241"
			if _, _, err := resolveManualTLS(context.Background(), bytes.NewReader(data), int64(len(data)), value, machine); err == nil {
				t.Fatal("unaligned ARM64 instruction accepted")
			}
		}
	}
}

func TestManualTLSRejectsHashArchitectureAndExecutableRangeChanges(t *testing.T) {
	for name, mutate := range map[string]func([]byte, *manualTLSOffsets){
		"hash mismatch": func(_ []byte, v *manualTLSOffsets) { v.SHA256 = strings.Repeat("0", 64) },
		"architecture":  func(data []byte, _ *manualTLSOffsets) { binary.LittleEndian.PutUint16(data[18:], uint16(elf.EM_ARM)) },
		"non-executable section": func(data []byte, _ *manualTLSOffsets) {
			binary.LittleEndian.PutUint64(data[0x800+64+8:], uint64(elf.SHF_ALLOC))
		},
		"non-executable mapping":      func(data []byte, _ *manualTLSOffsets) { binary.LittleEndian.PutUint32(data[68:], uint32(elf.PF_R)) },
		"instruction crosses mapping": func(data []byte, _ *manualTLSOffsets) { binary.LittleEndian.PutUint64(data[96:], 0x262) },
	} {
		t.Run(name, func(t *testing.T) {
			data := symbolFixture()
			binary.LittleEndian.PutUint16(data[18:], uint16(elf.EM_AARCH64))
			value := manualFixture(data, "/lib.so", "")
			mutate(data, &value)
			if name != "hash mismatch" {
				value.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
			}
			_, _, err := resolveManualTLS(context.Background(), bytes.NewReader(data), int64(len(data)), value, elf.EM_AARCH64)
			var rejected *manualTLSError
			if !errors.As(err, &rejected) {
				t.Fatalf("unsafe manual library accepted or diagnostic lost: %v", err)
			}
		})
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = resolveManualTLS(context.Background(), bytes.NewReader(data), int64(len(data)), manualFixture(data, path, ""), collectorMachine())
	if err == nil || !strings.Contains(err.Error(), "Go binaries require automatic Go ABI") {
		t.Fatalf("Go ABI gate was bypassed: %v", err)
	}
}

func TestManualAPKOffsetsUseTheOriginalInodeAndAddContainerBaseOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.apk")
	entry := writeAPK(t, path, zip.Store)
	value := manualFixture(symbolFixture(), path, entry)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = walkLibraryOptions(context.Background(), path, libraryOptions{offsets: []manualTLSOffsets{value}}, func(lib library) error {
		called = true
		if !strings.HasPrefix(lib.method, "manual") || lib.sha256 != value.SHA256 || lib.base == 0 || lib.entry != entry {
			t.Fatalf("manual APK selection lost: %+v", lib)
		}
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("updated package"), 0600); err != nil {
			t.Fatal(err)
		}
		pinned, err := os.Stat(lib.attachPath)
		if err != nil || !os.SameFile(before, pinned) {
			t.Fatal("manual verification did not pin the original inode", err)
		}
		probes, err := planProbes(lib)
		if err != nil {
			t.Fatal(err)
		}
		for _, probe := range probes {
			if probe.offset != lib.base+lib.symbols[probe.symbol] {
				t.Fatal("APK base added incorrectly", probe)
			}
		}
		return nil
	})
	if err != nil || !called {
		t.Fatal("manual APK was not visited", err)
	}
}

func TestManualAPKRejectsMissingCompressedAndMismatchedEntries(t *testing.T) {
	for _, mode := range []string{"missing", "compressed", "hash", "no entry", "wrong ABI"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "base.apk")
			method := uint16(zip.Store)
			if mode == "compressed" {
				method = zip.Deflate
			}
			entry := writeAPK(t, path, method)
			value := manualFixture(symbolFixture(), path, entry)
			switch mode {
			case "missing":
				value.Entry += ".so"
			case "hash":
				value.SHA256 = strings.Repeat("0", 64)
			case "no entry":
				value.Entry = ""
			case "wrong ABI":
				if strings.Contains(entry, "arm64-v8a") {
					value.Entry = strings.Replace(entry, "arm64-v8a", "x86_64", 1)
				} else {
					value.Entry = strings.Replace(entry, "x86_64", "arm64-v8a", 1)
				}
			}
			err := walkLibraryOptions(context.Background(), path, libraryOptions{offsets: []manualTLSOffsets{value}}, func(lib library) error {
				// A different entry can continue using automatic discovery.
				if mode == "hash" || mode == "no entry" || strings.HasPrefix(lib.method, "manual") {
					t.Fatal("rejected manual input fell back to auto resolution")
				}
				return nil
			})
			var rejected *manualTLSError
			if !errors.As(err, &rejected) || (value.Entry != "" && !strings.Contains(err.Error(), value.Entry)) {
				t.Fatalf("manual failure lost its APK entry: %v", err)
			}
		})
	}
}

func TestManualFailureDoesNotSuppressOtherAPKLibraries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.apk")
	entry := writeAPK(t, path, zip.Store)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range []string{entry, strings.Replace(entry, "libprivate.so", "libother.so", 1)} {
		member, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(symbolFixture()); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	value := manualFixture(symbolFixture(), path, entry)
	value.SHA256 = strings.Repeat("0", 64)
	var visited []string
	err := walkLibraryOptions(context.Background(), path, libraryOptions{offsets: []manualTLSOffsets{value}}, func(lib library) error {
		visited = append(visited, lib.entry)
		return nil
	})
	if err == nil || !reflect.DeepEqual(visited, []string{strings.Replace(entry, "libprivate.so", "libother.so", 1)}) {
		t.Fatal("manual failure hid other APK members", visited, err)
	}
}

func TestManualAPKEntryIsUniqueAndPreparedFirst(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "base.apk")
		entry := writeAPK(t, path, zip.Store)
		other := strings.Replace(entry, "libprivate.so", "libother.so", 1)
		names := []string{other, entry}
		if duplicate {
			names = append(names, entry)
		}
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		for _, name := range names {
			member, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := member.Write(symbolFixture()); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		var visited []string
		err := walkLibraryOptions(context.Background(), path, libraryOptions{offsets: []manualTLSOffsets{manualFixture(symbolFixture(), path, entry)}}, func(lib library) error {
			visited = append(visited, lib.entry)
			return nil
		})
		if duplicate {
			if err == nil || !strings.Contains(err.Error(), "duplicate entry names") || !reflect.DeepEqual(visited, []string{other}) {
				t.Fatal("ambiguous entry accepted or unrelated library suppressed", visited, err)
			}
		} else if err != nil || !reflect.DeepEqual(visited, []string{entry, other}) {
			t.Fatal("manual target waited for automatic analysis", visited, err)
		}
	}
}

func TestManualInspectionReportsHashesEvenWhenAutomaticResolutionFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "renamed.so")
	data := symbolFixture()
	binary.LittleEndian.PutUint32(data[0x800+2*64+4:], uint32(elf.SHT_PROGBITS))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	value := manualFixture(data, path, "")
	var output bytes.Buffer
	encode := json.NewEncoder(&output).Encode
	if err := inspectCommand(context.Background(), []string{"--inspect-library", path}, encode); err == nil {
		t.Fatal("automatic inspection unexpectedly succeeded")
	}
	if !strings.Contains(output.String(), value.SHA256) || !strings.Contains(output.String(), `"error":`) {
		t.Fatal("rejected library cannot be fingerprinted", output.String())
	}
	settings, err := json.Marshal([]manualTLSOffsets{value})
	if err != nil {
		t.Fatal(err)
	}
	overrides := filepath.Join(t.TempDir(), "offsets.json")
	if err := os.WriteFile(overrides, settings, 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := inspectCommand(context.Background(), []string{"--inspect-library", path, "--offsets", overrides}, encode); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"method":"manual SSL offsets`) {
		t.Fatal(output.String())
	}
	if _, err := librarySHA256(context.Background(), bytes.NewReader(data), int64(len(data)+1)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("short fingerprint read accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := resolveManualTLS(ctx, bytes.NewReader(data), int64(len(data)), value, collectorMachine()); !errors.Is(err, context.Canceled) {
		t.Fatal("manual resolution ignored cancellation", err)
	}
}

func TestCollectorRejectsMalformedManualConfigBeforeDiscovery(t *testing.T) {
	for _, mode := range []string{"missing", "duplicate", "unknown", "zero", "numeric", "overflow", "hash"} {
		value := manualFixture(symbolFixture(), "/library.so", "")
		values := []manualTLSOffsets{value}
		switch mode {
		case "missing":
			delete(value.Functions, "SSL_free")
		case "duplicate":
			values = append(values, value)
		case "unknown":
			value.Functions["GoTLS"] = "0x300"
		case "zero":
			value.Functions["SSL_read"] = "0x0"
		case "overflow":
			value.Functions["SSL_read"] = "0x10000000000000000"
		case "hash":
			values[0].SHA256 = "invalid"
		}
		data, err := json.Marshal(collectorConfig{TLSOffsets: values})
		if err != nil {
			t.Fatal(err)
		}
		if mode == "numeric" {
			data = bytes.ReplaceAll(data, []byte(`"0x200"`), []byte("512"))
		}
		if _, err := readConfig(bufio.NewReader(strings.NewReader(string(data) + "\n"))); err == nil {
			t.Fatal("invalid manual offsets accepted", mode)
		}
	}
}
