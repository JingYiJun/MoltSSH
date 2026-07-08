package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	var out bytes.Buffer
	if err := Run(nil, nil, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "moltssh proxy  --addr") {
		t.Fatalf("unexpected output: %q", got)
	}
	if got := out.String(); !strings.Contains(got, "--connect 127.0.0.1:22") {
		t.Fatalf("missing server connect flag: %q", got)
	}
	if got := out.String(); !strings.Contains(got, "--timeout 1s") {
		t.Fatalf("missing probe timeout flag: %q", got)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stderr bytes.Buffer
	err := Run([]string{"wat"}, nil, &bytes.Buffer{}, &stderr)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("missing usage: %q", stderr.String())
	}
}
