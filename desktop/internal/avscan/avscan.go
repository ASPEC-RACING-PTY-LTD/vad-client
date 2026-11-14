// Package avscan asks the antivirus installed on the PC to check content
// before VaultDock runs or writes it, through the Windows Antimalware Scan
// Interface (AMSI). Microsoft Defender and other antivirus products that
// register as AMSI providers (Malwarebytes, ESET, Sophos, ...) answer; with
// no provider the scan reports Clean.
package avscan

import "errors"

// Verdict is the antivirus' answer.
type Verdict int

const (
	// Unavailable: no scan happened (not Windows, AMSI off, or failed).
	Unavailable Verdict = iota
	// Clean: no provider objected.
	Clean
	// Blocked: the administrator's policy blocks this content.
	Blocked
	// Malware: a provider detected malware.
	Malware
)

func (v Verdict) String() string {
	switch v {
	case Clean:
		return "clean"
	case Blocked:
		return "blocked by policy"
	case Malware:
		return "malware detected"
	}
	return "not scanned"
}

// ErrDetected is returned by Check when the antivirus objects.
var ErrDetected = errors.New("the antivirus on this PC reported this file as harmful")

// maxScan is the most a single scan reads (installers are smaller).
const maxScan = 512 << 20

// Check scans the file at path and returns ErrDetected (wrapped with the
// verdict) when the antivirus objects. An unavailable scanner is not an
// error: the antivirus' own real-time protection still sees the file.
func Check(path, name string) (Verdict, error) {
	v, err := scanFile(path, name)
	if err != nil {
		return Unavailable, nil
	}
	if v == Malware || v == Blocked {
		return v, errors.Join(ErrDetected, errors.New(v.String()))
	}
	return v, nil
}
