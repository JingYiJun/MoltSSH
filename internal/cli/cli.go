package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/jingyijun/moltssh/internal/tunnel"
)

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stdout)
		return nil
	}

	switch args[0] {
	case "proxy":
		return runProxy(args[1:], stdin, stdout)
	case "server":
		return runServer(args[1:], stdout)
	case "probe":
		return runProbe(args[1:], stdout)
	case "help", "-h", "--help":
		usage(stdout)
		return nil
	default:
		usage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "MoltSSH: SSH ProxyCommand with migratable paths")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  moltssh proxy  --config ~/.config/moltssh/lab.toml")
	fmt.Fprintln(w, "  moltssh server --config /etc/moltssh/lab.toml")
	fmt.Fprintln(w, "  moltssh probe  --config ~/.config/moltssh/lab.toml")
}

func runProxy(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "TOML config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("proxy accepts no positional arguments")
	}
	cfg, err := tunnel.LoadConfigFile(*configPath, tunnel.CommandProxy)
	if err != nil {
		return err
	}
	return tunnel.Proxy(context.Background(), cfg, stdin, stdout)
}

func runServer(args []string, stdout io.Writer) error {
	_ = stdout
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "TOML config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("server accepts no positional arguments")
	}
	cfg, err := tunnel.LoadConfigFile(*configPath, tunnel.CommandServer)
	if err != nil {
		return err
	}
	return tunnel.Serve(context.Background(), cfg)
}

func runProbe(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "TOML config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("probe accepts no positional arguments")
	}
	cfg, err := tunnel.LoadConfigFile(*configPath, tunnel.CommandProbe)
	if err != nil {
		return err
	}
	return tunnel.Probe(context.Background(), cfg, stdout)
}
