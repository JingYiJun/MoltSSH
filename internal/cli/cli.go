package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/jingyijun/moltssh/internal/buildinfo"
	"github.com/jingyijun/moltssh/internal/tunnel"
)

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, info buildinfo.Info) error {
	if len(args) == 0 {
		rootUsage(stdout)
		return nil
	}

	switch args[0] {
	case "proxy":
		return runProxy(args[1:], stdin, stdout)
	case "server":
		return runServer(args[1:], stdout, stderr)
	case "probe":
		return runProbe(args[1:], stdout)
	case "version", "-v", "--version":
		if len(args) != 1 {
			return fmt.Errorf("version accepts no arguments\nhint: run \"moltssh help version\"")
		}
		printVersion(stdout, info)
		return nil
	case "help":
		return runHelp(args[1:], stdout)
	case "-h", "--help":
		if len(args) != 1 {
			return fmt.Errorf("--help accepts no arguments\nhint: run \"moltssh help COMMAND\"")
		}
		rootUsage(stdout)
		return nil
	default:
		rootUsage(stderr)
		return fmt.Errorf("unknown command %q\nhint: run \"moltssh --help\"", args[0])
	}
}

func runProxy(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "TOML config path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			proxyUsage(stdout)
			return nil
		}
		return fmt.Errorf("parse proxy arguments: %w\nhint: run \"moltssh help proxy\"", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("proxy accepts no positional arguments\nhint: run \"moltssh help proxy\"")
	}
	if *configPath == "" {
		return missingConfigError("proxy")
	}
	cfg, err := tunnel.LoadConfigFile(*configPath, tunnel.CommandProxy)
	if err != nil {
		return fmt.Errorf("load proxy config %q: %w\nhint: check the TOML file, then run \"moltssh probe --config %s\"", *configPath, err, *configPath)
	}
	if err := tunnel.Proxy(context.Background(), cfg, stdin, stdout); err != nil {
		return fmt.Errorf("proxy session failed: %w\nhint: run \"moltssh probe --config %s\" to identify the failing network phase", err, *configPath)
	}
	return nil
}

func runServer(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "TOML config path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			serverUsage(stdout)
			return nil
		}
		return fmt.Errorf("parse server arguments: %w\nhint: run \"moltssh help server\"", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("server accepts no positional arguments\nhint: run \"moltssh help server\"")
	}
	if *configPath == "" {
		return missingConfigError("server")
	}
	cfg, err := tunnel.LoadConfigFile(*configPath, tunnel.CommandServer)
	if err != nil {
		return fmt.Errorf("load server config %q: %w\nhint: verify server.listen and server.connect, then run \"moltssh help server\"", *configPath, err)
	}
	if warning := serverSecurityWarning(cfg.Server.Listen); warning != "" {
		fmt.Fprintln(stderr, warning)
	}
	if err := tunnel.Serve(context.Background(), cfg); err != nil {
		return fmt.Errorf("server failed: %w\nhint: verify that server.listen is available and server.connect reaches sshd", err)
	}
	return nil
}

func runProbe(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "TOML config path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			probeUsage(stdout)
			return nil
		}
		return fmt.Errorf("parse probe arguments: %w\nhint: run \"moltssh help probe\"", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("probe accepts no positional arguments\nhint: run \"moltssh help probe\"")
	}
	if *configPath == "" {
		return missingConfigError("probe")
	}
	cfg, err := tunnel.LoadConfigFile(*configPath, tunnel.CommandProbe)
	if err != nil {
		return fmt.Errorf("load probe config %q: %w\nhint: verify each paths[].endpoint and retry", *configPath, err)
	}
	if err := tunnel.Probe(context.Background(), cfg, stdout); err != nil {
		return fmt.Errorf("probe failed: %w\nhint: inspect failed_phase to distinguish DNS, TCP, TLS, WebSocket, and MoltSSH handshake failures", err)
	}
	return nil
}
