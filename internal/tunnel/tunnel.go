package tunnel

import (
	"context"
	"io"
	"net"
)

func Serve(ctx context.Context, cfg *Config) error {
	ln, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		return err
	}
	return serveListener(ctx, ln, cfg)
}

func Proxy(ctx context.Context, cfg *Config, stdin io.Reader, stdout io.Writer) error {
	rt := newClientRuntime(cfg, stdin, stdout)
	go func() {
		<-ctx.Done()
		rt.finish(ctx.Err())
	}()
	if err := rt.connectAny(ctx, false); err != nil {
		return err
	}
	go rt.stdinLoop(ctx)
	go rt.reconnectLoop(ctx)
	go rt.switchLoop(ctx)
	return rt.wait()
}
