package vless

import (
	"encoding/json"
	"strings"
)

// outboundFor builds the vless outbound (with its stream settings) for one node.
// It is the single source of truth for the outbound object, shared by ToJSON
// (a dokodemo-door relay pinned to one destination) and ProxyJSON (a general
// HTTP forward proxy).
func outboundFor(rawURL string) (outbound, error) {
	uuid, host, port, p, err := Parse(rawURL)
	if err != nil {
		return outbound{}, err
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
	return outbound{
		Protocol:       "vless",
		Settings:       outSet{Vnext: []vnext{{Address: host, Port: port, Users: []user{u}}}},
		StreamSettings: st,
	}, nil
}

// proxyConfig is the marshaled xray config used by ProxyJSON. It mirrors Config
// but its inbound carries an empty settings object ({}) as an HTTP proxy needs.
type proxyConfig struct {
	Log       logCfg         `json:"log"`
	Inbounds  []proxyInbound `json:"inbounds"`
	Outbounds []outbound     `json:"outbounds"`
}

type proxyInbound struct {
	Listen   string   `json:"listen"`
	Port     int      `json:"port"`
	Protocol string   `json:"protocol"`
	Settings struct{} `json:"settings"`
}

// ProxyJSON builds an xray config with an HTTP forward-proxy inbound on
// 127.0.0.1:httpPort routing all traffic out through the given vless node.
// Unlike ToJSON (a dokodemo-door pinned to one destination), this is a general
// forward proxy suitable for arbitrary HTTPS downloads (it follows CDN redirects).
func ProxyJSON(rawURL string, httpPort int) (string, error) {
	ob, err := outboundFor(rawURL)
	if err != nil {
		return "", err
	}
	cfg := proxyConfig{
		Log:       logCfg{Loglevel: "warning"},
		Inbounds:  []proxyInbound{{Listen: "127.0.0.1", Port: httpPort, Protocol: "http"}},
		Outbounds: []outbound{ob},
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
