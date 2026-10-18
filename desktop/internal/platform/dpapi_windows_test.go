//go:build windows

package platform

import (
	"bytes"
	"testing"
)

func TestDPAPIRoundTrip(t *testing.T) {
	secret := []byte("device key material")
	tests := []struct {
		name      string
		protect   func([]byte) ([]byte, error)
		unprotect func([]byte) ([]byte, error)
	}{
		{"user", Protect, Unprotect},
		{"machine-no-entropy",
			func(b []byte) ([]byte, error) { return ProtectMachine(b, nil) },
			func(b []byte) ([]byte, error) { return UnprotectMachine(b, nil) }},
		{"machine-entropy",
			func(b []byte) ([]byte, error) { return ProtectMachine(b, []byte("e1")) },
			func(b []byte) ([]byte, error) { return UnprotectMachine(b, []byte("e1")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := tt.protect(secret)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(enc, secret) {
				t.Fatal("ciphertext contains plaintext")
			}
			dec, err := tt.unprotect(enc)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dec, secret) {
				t.Fatalf("round trip = %q", dec)
			}
		})
	}
}

func TestMachineDPAPIWrongEntropy(t *testing.T) {
	enc, err := ProtectMachine([]byte("x"), []byte("right"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnprotectMachine(enc, []byte("wrong")); err == nil {
		t.Fatal("wrong entropy decrypted")
	}
	if _, err := UnprotectMachine(enc, nil); err == nil {
		t.Fatal("missing entropy decrypted")
	}
}
