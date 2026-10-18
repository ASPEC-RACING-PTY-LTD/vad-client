package guard

import (
	"fmt"
	"strings"
)

// cpath is a lexically canonical Windows path: a volume ("C:" or
// `\\server\share`) plus cleaned components.
type cpath struct {
	vol   string
	comps []string
}

// String renders the path; a drive root is "C:\", a share root
// `\\server\share`.
func (c cpath) String() string {
	if len(c.comps) == 0 {
		if isUNC(c.vol) {
			return c.vol
		}
		return c.vol + `\`
	}
	return c.vol + `\` + strings.Join(c.comps, `\`)
}

// prefix returns the path made of the volume and the first n components.
func (c cpath) prefix(n int) cpath { return cpath{vol: c.vol, comps: c.comps[:n]} }

func isUNC(vol string) bool { return strings.HasPrefix(vol, `\\`) }

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPath, fmt.Sprintf(format, a...))
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func isDriveAbs(p string) bool {
	if len(p) < 3 || p[1] != ':' || p[2] != '\\' {
		return false
	}
	ch := p[0] | 0x20
	return ch >= 'a' && ch <= 'z'
}

// lexical canonicalises p without touching the file system. It accepts
// only absolute drive (C:\…) and UNC (\\server\share\…) paths, optionally
// with a \\?\ or \\?\UNC\ prefix.
func lexical(p string) (cpath, error) {
	if p == "" {
		return cpath{}, invalid("empty path")
	}
	if strings.ContainsRune(p, 0) {
		return cpath{}, invalid("NUL in path")
	}
	p = strings.ReplaceAll(p, "/", `\`)
	verbatim := false
	switch {
	case hasPrefixFold(p, `\\?\UNC\`):
		p, verbatim = `\\`+p[len(`\\?\UNC\`):], true
	case strings.HasPrefix(p, `\\?\`):
		rest := p[len(`\\?\`):]
		if !isDriveAbs(rest) && !(len(rest) == 2 && rest[1] == ':') {
			return cpath{}, invalid("device or volume path")
		}
		p, verbatim = rest, true
	case strings.HasPrefix(p, `\\.\`), strings.HasPrefix(p, `\??\`):
		return cpath{}, invalid("device path")
	}

	var c cpath
	var rest string
	switch {
	case strings.HasPrefix(p, `\\`):
		parts := strings.SplitN(p[2:], `\`, 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return cpath{}, invalid("incomplete UNC path")
		}
		for _, part := range parts[:2] {
			if err := checkComponent(part); err != nil || part == "." || part == ".." || part == "?" {
				return cpath{}, invalid("bad UNC server or share")
			}
		}
		c.vol = `\\` + parts[0] + `\` + parts[1]
		if len(parts) == 3 {
			rest = parts[2]
		}
	case len(p) == 2 && p[1] == ':' && isDriveAbs(p+`\`):
		c.vol = strings.ToUpper(p[:1]) + ":"
	case isDriveAbs(p):
		c.vol = strings.ToUpper(p[:1]) + ":"
		rest = p[3:]
	default:
		return cpath{}, invalid("path is not absolute")
	}

	for _, comp := range strings.Split(rest, `\`) {
		switch comp {
		case "", ".":
			continue
		case "..":
			if len(c.comps) == 0 {
				return cpath{}, invalid("path escapes the volume root")
			}
			c.comps = c.comps[:len(c.comps)-1]
			continue
		}
		trimmed := strings.TrimRight(comp, ". ")
		if trimmed == "" {
			return cpath{}, invalid("component of only dots or spaces")
		}
		if verbatim && trimmed != comp {
			return cpath{}, invalid("trailing dot or space in verbatim path")
		}
		if err := checkComponent(trimmed); err != nil {
			return cpath{}, err
		}
		c.comps = append(c.comps, trimmed)
	}
	return c, nil
}

// checkComponent rejects alternate data streams, wildcard and control
// characters, and reserved DOS device names.
func checkComponent(s string) error {
	if strings.ContainsRune(s, ':') {
		return invalid("alternate data stream or stray colon")
	}
	for _, r := range s {
		if r < 0x20 || strings.ContainsRune(`*?"<>|`, r) {
			return invalid("invalid character in path")
		}
	}
	base := s
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return invalid("reserved device name")
	}
	if len(base) >= 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		switch base[3:] {
		case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return invalid("reserved device name")
		}
	}
	return nil
}
