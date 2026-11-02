package httpdebug

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func offsetSettings() TLSOffsetConfig {
	return TLSOffsetConfig{Path: "/data/app/example/base.apk", Entry: "lib/arm64-v8a/libprivate.so",
		SHA256: strings.Repeat("AB", 32), Functions: map[string]string{
			"SSL_new": "0X00001000", "SSL_free": "0x1100", "SSL_read": "0x1200", "SSL_write": "0x1300",
		}}
}

func TestTLSOffsetsSurviveSettingsValidationWithoutAddressRounding(t *testing.T) {
	value := offsetSettings()
	value.Functions["SSL_read_ex"] = "0xfffffffffffffff0"
	encoded, err := json.Marshal(Config{TLSOffsets: []TLSOffsetConfig{value}})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := Validate(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	var result Config
	if err := json.Unmarshal([]byte(validated), &result); err != nil {
		t.Fatal(err)
	}
	got := result.TLSOffsets[0]
	if got.Path != value.Path || got.Entry != value.Entry || got.SHA256 != strings.ToLower(value.SHA256) ||
		got.Functions["SSL_new"] != "0x1000" || got.Functions["SSL_read_ex"] != "0xfffffffffffffff0" {
		t.Fatalf("manual settings changed or lost precision: %+v", got)
	}
}

func TestTLSOffsetSettingsRejectIncompleteAndAmbiguousABIs(t *testing.T) {
	for name, mutate := range map[string]func(*TLSOffsetConfig){
		"relative path":                 func(v *TLSOffsetConfig) { v.Path = "lib.so" },
		"multiline path":                func(v *TLSOffsetConfig) { v.Path += "\nother" },
		"wrong APK ABI":                 func(v *TLSOffsetConfig) { v.Entry = "lib/armeabi-v7a/libssl.so" },
		"empty library name":            func(v *TLSOffsetConfig) { v.Entry = "lib/arm64-v8a/.so" },
		"APK traversal":                 func(v *TLSOffsetConfig) { v.Entry = "lib/arm64-v8a/../libssl.so" },
		"missing hash":                  func(v *TLSOffsetConfig) { v.SHA256 = "" },
		"bad hash":                      func(v *TLSOffsetConfig) { v.SHA256 = strings.Repeat("z", 64) },
		"missing lifetime":              func(v *TLSOffsetConfig) { delete(v.Functions, "SSL_free") },
		"Go ABI":                        func(v *TLSOffsetConfig) { v.Functions["crypto/tls.(*Conn).Read"] = "0x1400" },
		"unknown field":                 func(v *TLSOffsetConfig) { v.Functions["SSL_peek"] = "0x1400" },
		"decimal offset":                func(v *TLSOffsetConfig) { v.Functions["SSL_read"] = "4608" },
		"negative offset":               func(v *TLSOffsetConfig) { v.Functions["SSL_read"] = "-0x1200" },
		"zero offset":                   func(v *TLSOffsetConfig) { v.Functions["SSL_read"] = "0x0" },
		"overflow":                      func(v *TLSOffsetConfig) { v.Functions["SSL_read"] = "0x10000000000000000" },
		"same entry for different ABIs": func(v *TLSOffsetConfig) { v.Functions["SSL_read_ex"] = "0x1200" },
	} {
		t.Run(name, func(t *testing.T) {
			value := offsetSettings()
			mutate(&value)
			encoded, err := json.Marshal(Config{TLSOffsets: []TLSOffsetConfig{value}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Validate(string(encoded)); err == nil {
				t.Fatal("invalid manual offsets accepted")
			}
		})
	}
	value := offsetSettings()
	if err := validateTLSOffsets([]TLSOffsetConfig{value, value}); err == nil {
		t.Fatal("duplicate library accepted")
	}
	var many []TLSOffsetConfig
	for i := 0; i < 17; i++ {
		value := offsetSettings()
		value.Path = fmt.Sprintf("/library-%d.so", i)
		many = append(many, value)
	}
	if err := validateTLSOffsets(many); err == nil {
		t.Fatal("manual library limit ignored")
	}
	for _, input := range []string{
		`{"tlsOffsets":[{"path":"/lib.so","extra":true}]}`,
		`{"tlsOffsets":[{"functions":{"SSL_new":4096}}]}`,
	} {
		if _, err := Validate(input); err == nil {
			t.Fatal("non-string offset or unknown record field accepted")
		}
	}
}
