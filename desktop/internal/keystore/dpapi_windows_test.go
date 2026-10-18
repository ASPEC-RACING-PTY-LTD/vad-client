//go:build windows

package keystore

import "testing"

// TestMachineDPAPIBoxKey exercises the real machine-scope DPAPI protector.
// It skips when DPAPI is unavailable (for example a restricted runner).
func TestMachineDPAPIBoxKey(t *testing.T) {
	if _, err := (MachineDPAPI{}).Protect([]byte("probe"), boxEntropy); err != nil {
		t.Skipf("machine DPAPI unavailable: %v", err)
	}
	dir := t.TempDir()
	a, err := Open(Options{Dir: dir, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(Options{Dir: dir, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !a.BoxPub().Equal(b.BoxPub()) {
		t.Fatal("DPAPI round trip changed the box key")
	}
	if _, err := Open(Options{Dir: dir, Protector: fakeProtector{}, Logger: quiet}); err == nil {
		t.Fatal("DPAPI blob opened with another protector")
	}
}
