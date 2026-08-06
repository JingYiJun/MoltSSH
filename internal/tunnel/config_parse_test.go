package tunnel

import (
	"testing"
	"time"
)

func TestParseConfigProxy(t *testing.T) {
	cfg, err := ParseConfig(sampleConfig("127.0.0.1:1", "127.0.0.1:2"), CommandProxy)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "loop" || cfg.Probe.Timeout != time.Second || len(enabledPaths(cfg.Paths)) != 1 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := ParseConfig(`schema_version = 1
name = "loop"

[server]
listen = "127.0.0.1:8080"
connect = "127.0.0.1:22"

[[paths]]
name = "direct"
endpoint = "ws://127.0.0.1:8080/moltssh"
`, CommandProxy)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Resume.Timeout != 60*time.Second ||
		cfg.Resume.BufferBytes != 32*1024*1024 ||
		cfg.Probe.Interval != 3*time.Second ||
		cfg.Probe.Timeout != 2*time.Second ||
		cfg.Probe.SwitchCooldown != 10*time.Second ||
		cfg.Probe.ActiveFailureThreshold != 2 ||
		cfg.Probe.CandidateSuccessThreshold != 3 ||
		cfg.Probe.BetterRTTMinDelta != 30*time.Millisecond ||
		cfg.Probe.BetterRTTRatio != 0.25 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if got := cfg.Paths[0]; got.Transport != "ws" || got.Priority != 0 || !got.Enabled {
		t.Fatalf("unexpected path defaults: %+v", got)
	}

	cfg, err = ParseConfig(`schema_version = 1
name = "loop"

[server]
listen = "127.0.0.1:8080"
connect = "127.0.0.1:22"
`, CommandServer)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.HTTPPath != "/moltssh" {
		t.Fatalf("unexpected server http path default: %q", cfg.Server.HTTPPath)
	}
}

func TestParseConfigRejectsUnknownKey(t *testing.T) {
	_, err := ParseConfig(sampleConfig("127.0.0.1:1", "127.0.0.1:2")+"\nwat = true\n", CommandProxy)
	if err == nil {
		t.Fatal("expected error")
	}
}
