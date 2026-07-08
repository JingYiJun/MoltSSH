package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/jingyijun/moltssh/internal/tunnel"
)

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	_ = stdin
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
	fmt.Fprintln(w, "  moltssh proxy  --addr 127.0.0.1:4433")
	fmt.Fprintln(w, "  moltssh server --listen :4433")
	fmt.Fprintln(w, "  moltssh probe  --addr 127.0.0.1:4433")
}

func runProxy(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", "127.0.0.1:4433", "QUIC server address")
	insecure := fs.Bool("insecure", true, "skip server certificate verification")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return tunnel.Proxy(context.Background(), *addr, *insecure, stdin, stdout)
}

func runServer(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", ":4433", "QUIC listen address")
	connect := fs.String("connect", "127.0.0.1:22", "target TCP address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return tunnel.Serve(context.Background(), *listen, *connect)
}

func runProbe(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", "127.0.0.1:4433", "QUIC server address")
	timeout := fs.Duration("timeout", time.Second, "probe timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := tunnel.Probe(ctx, *addr); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "probe ok addr=%s\n", *addr)
	return nil
}
