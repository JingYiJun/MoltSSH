package tunnel

import (
	"bytes"
	"testing"
)

func TestReadConnect(t *testing.T) {
	br, err := readConnect(bytes.NewBufferString("CONNECT\n\nhello"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := br.ReadString('o'); got != "hello" {
		t.Fatalf("lost buffered payload: %q", got)
	}
}

func TestReadConnectRejectsBadHeader(t *testing.T) {
	if _, err := readConnect(bytes.NewBufferString("NOPE\n\n")); err == nil {
		t.Fatal("expected error")
	}
}
