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
	if got := out.String(); !strings.Contains(got, "moltssh proxy  --config") {
		t.Fatalf("unexpected output: %q", got)
	}
	if got := out.String(); !strings.Contains(got, "moltssh server --config") {
		t.Fatalf("missing server config usage: %q", got)
	}
	if got := out.String(); !strings.Contains(got, "moltssh probe  --config") {
		t.Fatalf("missing probe config usage: %q", got)
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
