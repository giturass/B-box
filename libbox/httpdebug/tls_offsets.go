package httpdebug

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// TLSOffsetConfig is a user-supplied public SSL ABI, bound to one ELF's bytes.
// Offsets are strings so Android/Flutter JSON never rounds a numeric address.
type TLSOffsetConfig struct {
	Path      string            `json:"path"`
	Entry     string            `json:"entry,omitempty"`
	SHA256    string            `json:"sha256"`
	Functions map[string]string `json:"functions"`
}

func validateTLSOffsets(values []TLSOffsetConfig) error {
	if len(values) > 16 {
		return errors.New("specify at most 16 manual TLS libraries")
	}
	seen := make(map[string]bool)
	for index := range values {
		value := &values[index]
		if err := value.validate(); err != nil {
			return fmt.Errorf("manual TLS offsets at index %d: %w", index, err)
		}
		key := value.Path + "\x00" + value.Entry
		if seen[key] {
			return fmt.Errorf("duplicate manual TLS library at index %d", index)
		}
		seen[key] = true
	}
	return nil
}

func (v *TLSOffsetConfig) validate() error {
	if !filepath.IsAbs(v.Path) || len(v.Path) > 4096 || strings.ContainsAny(v.Path, "\x00\r\n") {
		return errors.New("path must be an absolute ELF or APK path")
	}
	if v.Entry != "" {
		parts := strings.Split(v.Entry, "/")
		if len(v.Entry) > 512 || len(parts) != 3 || parts[0] != "lib" ||
			(parts[1] != "arm64-v8a" && parts[1] != "x86_64") || len(parts[2]) <= 3 || !strings.HasSuffix(parts[2], ".so") ||
			strings.ContainsAny(v.Entry, "\x00\r\n\\") {
			return errors.New("APK entry must be lib/arm64-v8a/name.so or lib/x86_64/name.so")
		}
	}
	hash, err := hex.DecodeString(v.SHA256)
	if err != nil || len(hash) != 32 {
		return errors.New("SHA-256 must contain 64 hexadecimal digits for the ELF, excluding its APK container")
	}
	v.SHA256 = strings.ToLower(v.SHA256)
	wanted := map[string]bool{
		"SSL_new": true, "SSL_free": true, "SSL_clear": true,
		"SSL_read": true, "SSL_write": true, "SSL_read_ex": true, "SSL_write_ex": true,
		"SSL_read_early_data": true, "SSL_write_early_data": true,
		"SSL_connect": true, "SSL_do_handshake": true,
	}
	seen := make(map[uint64]bool)
	for name, text := range v.Functions {
		if !wanted[name] {
			return fmt.Errorf("unsupported manual TLS function %.128q; only the public SSL ABI is accepted", name)
		}
		offset, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(text), "0x"), 16, 64)
		if err != nil || len(text) > 18 || !strings.HasPrefix(strings.ToLower(text), "0x") || offset == 0 {
			return fmt.Errorf("%s must be a nonzero hexadecimal ELF file offset (0x...)", name)
		}
		if seen[offset] {
			return errors.New("manual TLS function offsets must be distinct")
		}
		seen[offset] = true
		v.Functions[name] = fmt.Sprintf("%#x", offset)
	}
	for _, name := range []string{"SSL_new", "SSL_free", "SSL_read", "SSL_write"} {
		if v.Functions[name] == "" {
			return fmt.Errorf("manual TLS offsets require %s", name)
		}
	}
	return nil
}
