// Command s4uspike exercises the P0-S3 prototype.
//
//	s4uspike consent-selftest            # any user, no admin: DPAPI round trip
//	s4uspike profile -sid S-1-5-21-... -path C:\Users\x\file   # LocalSystem only (VM)
//
// "profile" must be run as LocalSystem (e.g. psexec -s) on a test VM.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/user"
	"time"

	"golang.org/x/sys/windows"

	"vdspike/s4u"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: s4uspike consent-selftest | profile -sid SID [-path FILE] [-dpapi BLOBFILE -device ID -consent ID]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "consent-selftest":
		err = consentSelftest()
	case "profile":
		err = profile(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func consentSelftest() error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	c := s4u.ConsentClaim{V: 1, SID: u.Uid, DeviceID: newID(), ConsentID: newID(), Time: time.Now().UTC(), Scope: "profile"}
	blob, err := s4u.ProtectConsent(c)
	if err != nil {
		return err
	}
	d, err := s4u.VerifyConsentSelf(blob, c)
	if err != nil {
		return err
	}
	fmt.Printf("protect+unprotect ok: blob %d bytes, proof_digest %x\n", len(blob), d)

	// Wrong device id => different entropy => must fail.
	wrong := c
	wrong.DeviceID = newID()
	if _, err := s4u.VerifyConsentSelf(blob, wrong); !errors.Is(err, s4u.ErrConsentInvalid) {
		return fmt.Errorf("wrong-entropy check: want ErrConsentInvalid, got %v", err)
	}
	fmt.Println("wrong device id rejected: ok")

	// Tamper one byte near the end (inside the HMAC-covered region).
	t := append([]byte(nil), blob...)
	t[len(t)-40] ^= 0xFF
	if _, err := s4u.VerifyConsentSelf(t, c); err == nil {
		return errors.New("tampered blob accepted")
	} else {
		fmt.Println("tampered blob rejected:", err)
	}
	return nil
}

func profile(args []string) error {
	fs := flag.NewFlagSet("profile", flag.ExitOnError)
	sid := fs.String("sid", "", "user SID")
	path := fs.String("path", "", "file to read as the user")
	blobFile := fs.String("dpapi", "", "consent blob file to verify")
	device := fs.String("device", "", "device id for consent entropy")
	consent := fs.String("consent", "", "consent id")
	_ = fs.Parse(args)

	t, err := s4u.ForProfile(*sid)
	if err != nil {
		return err
	}
	defer t.Close()
	fmt.Printf("token source=%s caps=%+v\n", t.Source(), t.Caps())

	if *path != "" {
		ok, err := t.AccessCheckPath(*path, windows.FILE_READ_DATA)
		fmt.Printf("AccessCheck(FILE_READ_DATA)=%v err=%v\n", ok, err)
		err = t.Do(func() error {
			f, err := os.Open(*path)
			if err != nil {
				return err
			}
			defer f.Close()
			buf := make([]byte, 64)
			n, err := f.Read(buf)
			fmt.Printf("read %d bytes as user\n", n)
			return err
		})
		fmt.Printf("open/read under impersonation err=%v\n", err)
	}
	if *blobFile != "" {
		blob, err := os.ReadFile(*blobFile)
		if err != nil {
			return err
		}
		d, err := s4u.VerifyConsent(t, blob, s4u.ConsentClaim{V: 1, SID: *sid, DeviceID: *device, ConsentID: *consent, Scope: "profile"})
		fmt.Printf("VerifyConsent digest=%x err=%v\n", d, err)
	}
	return nil
}
