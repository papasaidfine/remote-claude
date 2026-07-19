// Package relay implements the `remote-claude relay <host> <port>` subcommand:
// the ssh ProxyCommand helper. It picks a random vless node, starts a
// per-connection xray with a dokodemo-door inbound, and pumps the ssh byte
// stream (stdin/stdout) through it. xray dies with the connection.
//
// This one subcommand replaces, across all platforms, the Windows C#
// rc-stdio-relay.exe + xray-proxy.ps1 and the macOS `nc -x` launcher.
package relay

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/papasaidfine/remote-claude/internal/nodes"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/vless"
	"github.com/papasaidfine/remote-claude/internal/xray"
)

// Main is the entry point for the relay subcommand. It returns a process exit
// code. All diagnostics go to stderr; stdout is the data channel.
func Main(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: remote-claude relay <host> <port>")
		return 2
	}
	host := args[0]
	destPort, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote-claude relay: bad port %q\n", args[1])
		return 2
	}
	if err := run(host, destPort); err != nil {
		fmt.Fprintln(os.Stderr, "remote-claude proxy: "+err.Error())
		return 1
	}
	return 0
}

func run(host string, destPort int) error {
	p, err := paths.Resolve()
	if err != nil {
		return err
	}
	xrayBin := xray.Resolve(p)
	if xrayBin == "" {
		return fmt.Errorf("xray binary not found — re-run bootstrap item 7")
	}
	node, err := nodes.PickRandom(p.VlessNodes)
	if err != nil {
		return err
	}
	dokoPort, err := reservePort()
	if err != nil {
		return err
	}
	cfgJSON, err := vless.ToJSON(node, dokoPort, host, destPort)
	if err != nil {
		return err
	}

	cfgPath := filepath.Join(os.TempDir(), fmt.Sprintf("rc-xray-%d.json", os.Getpid()))
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		return err
	}
	cfgRemoved := false
	defer func() {
		if !cfgRemoved {
			os.Remove(cfgPath)
		}
	}()

	cmd := exec.Command(xrayBin, "run", "-c", cfgPath)
	cmd.Stdin = nil
	cmd.Stdout = nil // xray logs to stderr per its config; keep our stdout clean
	cmd.Stderr = os.Stderr
	setSysProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start xray: %w", err)
	}
	afterStart(cmd)
	defer kill(cmd)

	conn, err := connectInbound(cmd, dokoPort)
	if err != nil {
		return err
	}
	defer conn.Close()

	// The inbound is up; the config file is no longer needed.
	os.Remove(cfgPath)
	cfgRemoved = true

	pump(conn)
	return nil
}

// reservePort grabs a free loopback TCP port for the dokodemo-door inbound.
func reservePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// connectInbound waits for xray's inbound to accept, retrying briefly.
func connectInbound(cmd *exec.Cmd, port int) (net.Conn, error) {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for i := 0; i < 50; i++ {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil, fmt.Errorf("xray exited before its inbound came up")
		}
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			if tcp, ok := conn.(*net.TCPConn); ok {
				tcp.SetNoDelay(true)
			}
			return conn, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("xray did not come up")
}

// pump copies stdin→conn and conn→stdout, returning when either direction ends.
func pump(conn net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(conn, os.Stdin); done <- struct{}{} }()
	go func() { io.Copy(os.Stdout, conn); done <- struct{}{} }()
	<-done
}
