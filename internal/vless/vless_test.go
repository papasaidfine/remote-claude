package vless

import (
	"strings"
	"testing"
)

func toJSON(t *testing.T, url string) string {
	t.Helper()
	j, err := ToJSON(url, 20800, "dest.host", 22)
	if err != nil {
		t.Fatalf("ToJSON(%q) error: %v", url, err)
	}
	return j
}

func has(t *testing.T, j, needle string) {
	t.Helper()
	if !strings.Contains(j, needle) {
		t.Errorf("missing %q in:\n%s", needle, j)
	}
}

func TestRealityTCPVision(t *testing.T) {
	u := "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=reality&pbk=PUBKEYXYZ&sid=ab12&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#node"
	j := toJSON(t, u)
	has(t, j, `"id": "11111111-2222-3333-4444-555555555555"`)
	has(t, j, `"address": "example.com"`)
	has(t, j, `"port": 443`)
	has(t, j, `"security": "reality"`)
	has(t, j, `"publicKey": "PUBKEYXYZ"`)
	has(t, j, `"shortId": "ab12"`)
	has(t, j, `"serverName": "www.microsoft.com"`)
	has(t, j, `"flow": "xtls-rprx-vision"`)
	// dokodemo-door inbound wired to the relay destination
	has(t, j, `"protocol": "dokodemo-door"`)
	has(t, j, `"address": "dest.host"`)
}

func TestTLSWithWS(t *testing.T) {
	u := "vless://aaaa@host.tld:8443?type=ws&security=tls&sni=host.tld&path=%2Fws&host=host.tld"
	j := toJSON(t, u)
	has(t, j, `"security": "tls"`)
	has(t, j, `"path": "/ws"`)
	has(t, j, `"Host": "host.tld"`)
}

func TestDefaultsAndGRPC(t *testing.T) {
	j := toJSON(t, "vless://uid@h:2053?type=grpc&security=tls&serviceName=gs")
	has(t, j, `"serviceName": "gs"`)
	has(t, j, `"fingerprint": "chrome"`) // default fp filled in
}

func TestUnsupportedSecurityErrors(t *testing.T) {
	if _, err := ToJSON("vless://x@h:1?security=weird&type=tcp", 1, "d", 22); err == nil {
		t.Error("expected error for unsupported security")
	}
}

func TestUnsupportedTypeErrors(t *testing.T) {
	if _, err := ToJSON("vless://x@h:1?type=quic&security=none", 1, "d", 22); err == nil {
		t.Error("expected error for unsupported network type")
	}
}

func TestRealityRequiresPbk(t *testing.T) {
	if err := Validate("vless://x@h:443?type=tcp&security=reality&sni=a"); err == nil {
		t.Error("expected error when reality URL omits pbk")
	}
}

func TestNotVlessErrors(t *testing.T) {
	if err := Validate("https://example.com"); err == nil {
		t.Error("expected error for non-vless URL")
	}
}
