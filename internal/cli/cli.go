package cli

import (
	"flag"
	"fmt"
	"io"
)

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	_ = stdin
	if len(args) == 0 {
		usage(stdout)
		return nil
	}

	switch args[0] {
	case "proxy":
		return runProxy(args[1:], stdout)
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
	fmt.Fprintln(w, "  moltssh proxy  --config moltssh.yaml")
	fmt.Fprintln(w, "  moltssh server --listen :4433")
	fmt.Fprintln(w, "  moltssh probe  --config moltssh.yaml")
}

func runProxy(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config := fs.String("config", "moltssh.yaml", "config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "proxy config=%s\n", *config)
	return nil
}

func runServer(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", ":4433", "QUIC listen address")
	connect := fs.String("connect", "127.0.0.1:22", "target TCP address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "server listen=%s connect=%s\n", *listen, *connect)
	return nil
}

func runProbe(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config := fs.String("config", "moltssh.yaml", "config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "probe config=%s\n", *config)
	return nil
}
