// Package vless converts a vless:// share URL into an xray client config. The
// inbound is a dokodemo-door that the relay points at the real ssh destination;
// the outbound is the fully-resolved VLESS node.
package vless

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var urlRe = regexp.MustCompile(`^([^@]+)@(.+):(\d+)(\?(.*))?$`)

type params struct {
	typ, security, flow, sni, fp, pbk, sid, alpn, path, hosthdr, servicename string
}

// Config is the marshaled xray config; fields use omitempty so absent transport
// or security settings drop out cleanly.
type Config struct {
	Log       logCfg     `json:"log"`
	Inbounds  []inbound  `json:"inbounds"`
	Outbounds []outbound `json:"outbounds"`
}

type logCfg struct {
	Loglevel string `json:"loglevel"`
}
type inbound struct {
	Listen   string     `json:"listen"`
	Port     int        `json:"port"`
	Protocol string     `json:"protocol"`
	Settings inboundSet `json:"settings"`
}
type inboundSet struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Network string `json:"network"`
}
type outbound struct {
	Protocol       string `json:"protocol"`
	Settings       outSet `json:"settings"`
	StreamSettings stream `json:"streamSettings"`
}
type outSet struct {
	Vnext []vnext `json:"vnext"`
}
type vnext struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Users   []user `json:"users"`
}
type user struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
	Flow       string `json:"flow,omitempty"`
}
type stream struct {
	Network         string      `json:"network"`
	Security        string      `json:"security"`
	RealitySettings *realitySet `json:"realitySettings,omitempty"`
	TLSSettings     *tlsSet     `json:"tlsSettings,omitempty"`
	WSSettings      *wsSet      `json:"wsSettings,omitempty"`
	GRPCSettings    *grpcSet    `json:"grpcSettings,omitempty"`
	TCPSettings     *struct{}   `json:"tcpSettings,omitempty"`
}
type realitySet struct {
	ServerName  string `json:"serverName"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId"`
	SpiderX     string `json:"spiderX"`
}
type tlsSet struct {
	ServerName  string   `json:"serverName"`
	Fingerprint string   `json:"fingerprint"`
	Alpn        []string `json:"alpn"`
}
type wsSet struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
}
type grpcSet struct {
	ServiceName string `json:"serviceName"`
}

// Parse validates a vless:// URL and returns its resolved outbound components.
// It is exported so the nodes file can be validated without building full JSON.
func Parse(rawURL string) (uuid, host string, port int, p params, err error) {
	if !strings.HasPrefix(rawURL, "vless://") {
		return "", "", 0, params{}, fmt.Errorf("not a vless:// URL")
	}
	rest := strings.TrimPrefix(rawURL, "vless://")
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	m := urlRe.FindStringSubmatch(rest)
	if m == nil {
		return "", "", 0, params{}, fmt.Errorf("malformed vless:// URL (need uuid@host:port)")
	}
	uuid, host = m[1], m[2]
	port, _ = strconv.Atoi(m[3])

	p = params{typ: "tcp", security: "none"}
	if m[5] != "" {
		q, _ := url.ParseQuery(m[5])
		get := func(k string) string { return q.Get(k) }
		if v := get("type"); v != "" {
			p.typ = v
		}
		if v := get("network"); v != "" {
			p.typ = v
		}
		if v := get("security"); v != "" {
			p.security = v
		}
		p.flow = get("flow")
		p.sni = get("sni")
		p.fp = get("fp")
		p.pbk = get("pbk")
		p.sid = get("sid")
		p.alpn = get("alpn")
		p.path = get("path")
		p.hosthdr = get("host")
		p.servicename = get("serviceName")
	}
	if p.security == "" {
		p.security = "none"
	}
	switch p.security {
	case "reality", "tls", "none":
	default:
		return "", "", 0, params{}, fmt.Errorf("unsupported security=%q (supported: reality, tls, none)", p.security)
	}
	switch p.typ {
	case "tcp", "ws", "grpc":
	default:
		return "", "", 0, params{}, fmt.Errorf("unsupported network type=%q (supported: tcp, ws, grpc)", p.typ)
	}
	if p.security == "reality" && p.pbk == "" {
		return "", "", 0, params{}, fmt.Errorf("reality requires pbk (publicKey) in the URL")
	}
	return uuid, host, port, p, nil
}

// Validate parses the URL and discards the result, returning any error.
func Validate(rawURL string) error {
	_, _, _, _, err := Parse(rawURL)
	return err
}

// ToJSON builds the full xray config JSON for one node, with the dokodemo-door
// inbound listening on dokoPort and forwarding to destHost:destPort.
func ToJSON(rawURL string, dokoPort int, destHost string, destPort int) (string, error) {
	uuid, host, port, p, err := Parse(rawURL)
	if err != nil {
		return "", err
	}
	u := user{ID: uuid, Encryption: "none", Flow: p.flow}
	st := stream{Network: p.typ, Security: p.security}
	switch p.security {
	case "reality":
		fp := p.fp
		if fp == "" {
			fp = "chrome"
		}
		st.RealitySettings = &realitySet{
			ServerName: p.sni, Fingerprint: fp, PublicKey: p.pbk, ShortID: p.sid, SpiderX: "",
		}
	case "tls":
		fp := p.fp
		if fp == "" {
			fp = "chrome"
		}
		var alpn []string
		if p.alpn != "" {
			alpn = strings.Split(p.alpn, ",")
		} else {
			alpn = []string{}
		}
		st.TLSSettings = &tlsSet{ServerName: p.sni, Fingerprint: fp, Alpn: alpn}
	}
	switch p.typ {
	case "ws":
		path := p.path
		if path == "" {
			path = "/"
		}
		st.WSSettings = &wsSet{Path: path, Headers: map[string]string{"Host": p.hosthdr}}
	case "grpc":
		st.GRPCSettings = &grpcSet{ServiceName: p.servicename}
	case "tcp":
		st.TCPSettings = &struct{}{}
	}

	cfg := Config{
		Log: logCfg{Loglevel: "warning"},
		Inbounds: []inbound{{Listen: "127.0.0.1", Port: dokoPort, Protocol: "dokodemo-door",
			Settings: inboundSet{Address: destHost, Port: destPort, Network: "tcp"}}},
		Outbounds: []outbound{{Protocol: "vless",
			Settings:       outSet{Vnext: []vnext{{Address: host, Port: port, Users: []user{u}}}},
			StreamSettings: st}},
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
