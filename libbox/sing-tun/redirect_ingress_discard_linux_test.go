package tun

import (
	"encoding/binary"
	"testing"

	"golang.org/x/sys/unix"
)

func TestAndroidIngressDiscardMapLayouts(t *testing.T) {
	for _, test := range []struct {
		name      string
		mapType   uint32
		keySize   uint32
		valueSize uint32
		valid     bool
	}{
		{"single interface", unix.BPF_MAP_TYPE_HASH, 16, 4, true},
		{"two interfaces", unix.BPF_MAP_TYPE_HASH, 16, 8, true},
		{"wrong map type", unix.BPF_MAP_TYPE_ARRAY, 16, 4, false},
		{"wrong key size", unix.BPF_MAP_TYPE_HASH, 4, 4, false},
		{"empty value", unix.BPF_MAP_TYPE_HASH, 16, 0, false},
		{"short value", unix.BPF_MAP_TYPE_HASH, 16, 2, false},
		{"unaligned value", unix.BPF_MAP_TYPE_HASH, 16, 6, false},
		{"unknown value", unix.BPF_MAP_TYPE_HASH, 16, 16, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateAndroidIngressDiscardMapLayout(test.mapType, test.keySize, test.valueSize)
			if (err == nil) != test.valid {
				t.Fatalf("valid = %v, error = %v", test.valid, err)
			}
		})
	}
}

func TestAndroidIngressDiscardInterfaceOwnership(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   []byte
		ifIndex int
		matches bool
	}{
		{"single tun interface", ingressDiscardTestValue(27), 27, true},
		{"another single interface", ingressDiscardTestValue(28), 27, false},
		{"both tun interfaces", ingressDiscardTestValue(27, 27), 27, true},
		{"another first interface", ingressDiscardTestValue(28, 27), 27, false},
		{"another second interface", ingressDiscardTestValue(27, 28), 27, false},
		{"empty second slot", ingressDiscardTestValue(27, 0), 27, false},
		{"missing value", nil, 27, false},
		{"truncated value", []byte{27, 0, 0}, 27, false},
		{"extra slot", ingressDiscardTestValue(27, 27, 27), 27, false},
		{"zero interface", ingressDiscardTestValue(0), 0, false},
		{"invalid interface", ingressDiscardTestValue(27), -1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := androidIngressDiscardValueMatchesInterface(test.value, test.ifIndex); got != test.matches {
				t.Fatalf("matches = %v, want %v", got, test.matches)
			}
		})
	}
}

func TestAndroidIngressDiscardRestoreLayout(t *testing.T) {
	for _, test := range []struct {
		name      string
		value     []byte
		valueSize int
		ifIndex   int
		restore   bool
	}{
		{"restore single interface", ingressDiscardTestValue(27), 4, 27, true},
		{"restore two interfaces", ingressDiscardTestValue(27, 27), 8, 27, true},
		{"map grew", ingressDiscardTestValue(27), 8, 27, false},
		{"map shrank", ingressDiscardTestValue(27, 27), 4, 27, false},
		{"tun was replaced", ingressDiscardTestValue(27), 4, 28, false},
		{"another second interface", ingressDiscardTestValue(27, 28), 8, 27, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := androidIngressDiscardValueCanRestore(test.value, test.valueSize, test.ifIndex); got != test.restore {
				t.Fatalf("restore = %v, want %v", got, test.restore)
			}
		})
	}
}

func ingressDiscardTestValue(interfaces ...uint32) []byte {
	value := make([]byte, 4*len(interfaces))
	for i, ifIndex := range interfaces {
		binary.NativeEndian.PutUint32(value[i*4:], ifIndex)
	}
	return value
}
