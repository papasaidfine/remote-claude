package sshcfg

import "strings"

// Summary is a host block flattened for display: the common keys the UI shows,
// plus flags. Unknown keys stay in the Block and are edited there.
type Summary struct {
	Alias         string   `json:"alias"`
	Patterns      []string `json:"patterns"`
	HostName      string   `json:"hostname"`
	User          string   `json:"user"`
	Port          string   `json:"port"`
	RemoteForward string   `json:"remote_forward"` // raw RemoteForward value, "" if none
	ReversePort   string   `json:"reverse_port"`   // bind port parsed from RemoteForward
	ProxyCommand  string   `json:"proxy_command"`
	IdentityFile  string   `json:"identity_file"`
	HasReverse    bool     `json:"has_reverse"`
	HasProxy      bool     `json:"has_proxy"`
}

// Summary flattens the block's common keys for display.
func (b *Block) Summary() Summary {
	rf := b.Get("RemoteForward")
	return Summary{
		Alias:         b.Alias(),
		Patterns:      b.Patterns,
		HostName:      b.Get("HostName"),
		User:          b.Get("User"),
		Port:          b.Get("Port"),
		RemoteForward: rf,
		ReversePort:   reversePort(rf),
		ProxyCommand:  b.Get("ProxyCommand"),
		IdentityFile:  b.Get("IdentityFile"),
		HasReverse:    rf != "",
		HasProxy:      b.Has("ProxyCommand"),
	}
}

// reversePort extracts the bind port from a RemoteForward value, whose first
// field is "[bind_address:]port".
func reversePort(remoteForward string) string {
	f := strings.Fields(remoteForward)
	if len(f) == 0 {
		return ""
	}
	bind := f[0]
	if i := strings.LastIndex(bind, ":"); i >= 0 {
		return bind[i+1:]
	}
	return bind
}
