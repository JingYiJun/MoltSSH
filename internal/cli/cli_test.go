package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunProxy(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"proxy", "--config", "test.yaml"}, nil, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "proxy config=test.yaml") {
		t.Fatalf("unexpected output: %q", got)
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
