package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"math/big"
	"path/filepath"
	"testing"
)

func TestAppLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "applock.dat")
	l, err := openAppLock(path)
	if err != nil {
		t.Fatal(err)
	}
	_, dev, _ := ed25519.GenerateKey(rand.Reader)
	if err := l.setup(SetupLockRequest{PIN: "12345"}, dev); err == nil {
		t.Fatal("5-digit PIN accepted")
	}
	if err := l.setup(SetupLockRequest{PIN: "123456"}, dev); err != nil {
		t.Fatal(err)
	}
	if k, err := l.deviceKey(); err != nil || !k.Equal(dev) {
		t.Fatalf("device key after setup: %v", err)
	}

	// A restart starts locked.
	l, err = openAppLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if !l.locked() {
		t.Fatal("not locked after a restart")
	}
	if _, err := l.deviceKey(); err != ErrLocked {
		t.Fatalf("device key while locked: %v", err)
	}
	if err := l.unlock(UnlockRequest{PIN: "000000"}); err == nil {
		t.Fatal("wrong PIN unlocked")
	}
	if err := l.unlock(UnlockRequest{Secret: make([]byte, 32)}); err == nil {
		t.Fatal("wrong Hello secret unlocked")
	}
	if err := l.unlock(UnlockRequest{PIN: "123456"}); err != nil {
		t.Fatal(err)
	}
	if k, err := l.deviceKey(); err != nil || !k.Equal(dev) {
		t.Fatalf("device key after unlock: %v", err)
	}

	// Windows Hello: the lock key is encrypted to its RSA key.
	hk, _ := rsa.GenerateKey(rand.Reader, 2048)
	if err := l.setHello(HelloRequest{KeyName: "k", Modulus: hk.N.Bytes(), Exponent: big.NewInt(int64(hk.E)).Bytes()}); err != nil {
		t.Fatal(err)
	}
	st := l.status()
	secret, err := rsa.DecryptPKCS1v15(nil, hk, st.HelloBox)
	if err != nil {
		t.Fatal(err)
	}
	l.lock()
	if err := l.unlock(UnlockRequest{Secret: secret}); err != nil {
		t.Fatal(err)
	}

	// Changing the PIN needs the current one.
	if err := l.setup(SetupLockRequest{PIN: "654321", CurrentPIN: "111111"}, nil); err == nil {
		t.Fatal("PIN changed without the current PIN")
	}
	if err := l.setup(SetupLockRequest{PIN: "654321", CurrentPIN: "123456"}, nil); err != nil {
		t.Fatal(err)
	}
	l.lock()
	if err := l.unlock(UnlockRequest{PIN: "654321"}); err != nil {
		t.Fatal(err)
	}
	if k, err := l.deviceKey(); err != nil || !k.Equal(dev) {
		t.Fatalf("device key after PIN change: %v", err)
	}
}

func TestAppLockWaitsAfterWrongPINs(t *testing.T) {
	l, _ := openAppLock(filepath.Join(t.TempDir(), "applock.dat"))
	if err := l.setup(SetupLockRequest{PIN: "123456"}, nil); err != nil {
		t.Fatal(err)
	}
	l.lock()
	for i := 0; i < freeTries; i++ {
		_ = l.unlock(UnlockRequest{PIN: "999999"})
	}
	if l.status().RetryAfter == 0 {
		t.Fatal("no wait after repeated wrong PINs")
	}
	if err := l.unlock(UnlockRequest{PIN: "123456"}); err == nil {
		t.Fatal("unlocked during the wait")
	}
}
