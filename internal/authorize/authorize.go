// Package authorize appends the server's connect-back public key to
// authorized_keys, restricted to loopback logins and de-duplicated by key blob.
package authorize

import (
	"fmt"
	"os"
	"strings"
)

// Blob returns the base64 key body (the first AAAA… field) of a public key.
func Blob(pubkey string) (string, error) {
	for _, f := range strings.Fields(pubkey) {
		if strings.HasPrefix(f, "AAAA") {
			return f, nil
		}
	}
	return "", fmt.Errorf("could not parse the key data from the public key")
}

// Entry renders the authorized_keys line for pubkey, restricted to loopback.
func Entry(pubkey string) string {
	return `from="127.0.0.1,::1",no-agent-forwarding,no-X11-forwarding ` + strings.TrimSpace(pubkey)
}

// Add validates pubkey (via the injected validate func, may be nil to skip),
// then appends its loopback-restricted entry to authKeys unless its blob is
// already present. Returns whether a new line was written.
func Add(authKeys, pubkey string, validate func(pubkey string) error) (bool, error) {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return false, fmt.Errorf("no key pasted; nothing changed")
	}
	if validate != nil {
		if err := validate(pubkey); err != nil {
			return false, fmt.Errorf("the pasted content is not a valid SSH public key; please check and re-run")
		}
	}
	blob, err := Blob(pubkey)
	if err != nil {
		return false, err
	}
	if existing, err := os.ReadFile(authKeys); err == nil && strings.Contains(string(existing), blob) {
		return false, nil // already authorized
	}
	f, err := os.OpenFile(authKeys, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.WriteString(Entry(pubkey) + "\n"); err != nil {
		return false, err
	}
	return true, nil
}
