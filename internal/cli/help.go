package cli

import (
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/jingyijun/moltssh/internal/buildinfo"
)

func rootUsage(w io.Writer) {
	fmt.Fprintln(w, "MoltSSH: resumable OpenSSH ProxyCommand over WebSocket")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  moltssh COMMAND [OPTIONS]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  proxy     Run as an OpenSSH ProxyCommand")
	fmt.Fprintln(w, "  server    Keep the stable TCP connection to sshd")
	fmt.Fprintln(w, "  probe     Check configured WebSocket paths and timing phases")
	fmt.Fprintln(w, "  version   Print version, commit, and Go toolchain")
	fmt.Fprintln(w, "  help      Show help for a command")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run \"moltssh help COMMAND\" for command-specific help.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Security:")
	fmt.Fprintln(w, "  MoltSSH has no application-layer authentication. Keep the raw server")
	fmt.Fprintln(w, "  listener on loopback or behind a protected private access layer.")
}

func runHelp(args []string, w io.Writer) error {
	if len(args) == 0 {
		rootUsage(w)
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("help accepts at most one command\nhint: run \"moltssh --help\"")
	}
	switch args[0] {
	case "proxy":
		proxyUsage(w)
	case "server":
		serverUsage(w)
	case "probe":
		probeUsage(w)
	case "version":
		versionUsage(w)
	default:
		return fmt.Errorf("unknown help topic %q\nhint: run \"moltssh --help\"", args[0])
	}
	return nil
}

func proxyUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  moltssh proxy --config FILE")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run MoltSSH as an OpenSSH ProxyCommand using paths from FILE.")
	fmt.Fprintln(w, "Troubleshooting: run \"moltssh probe --config FILE\" to inspect DNS, TCP,")
	fmt.Fprintln(w, "TLS, WebSocket upgrade, and MoltSSH handshake timing.")
}

func serverUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  moltssh server --config FILE")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Serve the resumable WebSocket protocol and keep a stable TCP connection to sshd.")
	fmt.Fprintln(w, "Security: the server has no application-layer authentication. Bind to loopback")
	fmt.Fprintln(w, "or place it behind a protected reverse proxy or private access layer.")
}

func probeUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  moltssh probe --config FILE")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Probe every enabled WebSocket path and print phase timings.")
	fmt.Fprintln(w, "Use failed_phase to identify DNS, TCP, TLS, WebSocket, or protocol failures.")
}

func versionUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  moltssh version")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Print the MoltSSH version, source commit, and Go toolchain version.")
}

func printVersion(w io.Writer, info buildinfo.Info) {
	fmt.Fprintln(w, "moltssh")
	fmt.Fprintf(w, "  version: %s\n", info.Version)
	fmt.Fprintf(w, "  commit: %s\n", info.Commit)
	fmt.Fprintf(w, "  go: %s\n", info.GoVersion)
}

func missingConfigError(command string) error {
	return fmt.Errorf("%s requires --config FILE\nhint: run \"moltssh help %s\"", command, command)
}

func serverSecurityWarning(listen string) string {
	host, _, err := net.SplitHostPort(listen)
	if err == nil && (strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()) {
		return ""
	}
	return fmt.Sprintf(
		"warning: server.listen=%q is not loopback and MoltSSH has no application-layer authentication; place the raw listener behind a protected reverse proxy or private access layer",
		listen,
	)
}
