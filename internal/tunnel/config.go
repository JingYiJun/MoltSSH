package tunnel

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	CommandProxy  = "proxy"
	CommandServer = "server"
	CommandProbe  = "probe"
)

const (
	defaultServerHTTPPath                 = "/moltssh"
	defaultResumeTimeout                  = 60 * time.Second
	defaultResumeBufferBytes              = 32 * 1024 * 1024
	defaultProbeInterval                  = 3 * time.Second
	defaultProbeTimeout                   = 2 * time.Second
	defaultProbeSwitchCooldown            = 10 * time.Second
	defaultProbeActiveFailureThreshold    = 2
	defaultProbeCandidateSuccessThreshold = 3
	defaultProbeBetterRTTMinDelta         = 30 * time.Millisecond
	defaultProbeBetterRTTRatio            = 0.25
	defaultPathTransport                  = "ws"
	defaultPathPriority                   = 0
	defaultPathEnabled                    = true
)

type Config struct {
	SchemaVersion  int
	Name           string
	Server         ServerConfig
	Resume         ResumeConfig
	Probe          ProbeConfig
	Paths          []PathConfig
	sourceIdentity string
}

type ServerConfig struct {
	Listen   string
	HTTPPath string
	Connect  string
}

type ResumeConfig struct {
	Timeout     time.Duration
	BufferBytes int
}

type ProbeConfig struct {
	Interval                  time.Duration
	Timeout                   time.Duration
	SwitchCooldown            time.Duration
	ActiveFailureThreshold    int
	CandidateSuccessThreshold int
	BetterRTTMinDelta         time.Duration
	BetterRTTRatio            float64
}

type PathConfig struct {
	Name      string
	Transport string
	Endpoint  string
	Priority  int
	Enabled   bool
}

func LoadConfigFile(path, command string) (*Config, error) {
	if path == "" {
		return nil, fmt.Errorf("--config is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := ParseConfig(string(data), command)
	if err != nil {
		return nil, err
	}
	if command == CommandProxy {
		cfg.sourceIdentity = canonicalConfigIdentity(path)
	}
	return cfg, nil
}

func canonicalConfigIdentity(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	return abs
}

func ParseConfig(data, command string) (*Config, error) {
	raw, err := parseTOML(data)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	cfg.SchemaVersion, err = requiredInt(raw.top, "schema_version")
	if err != nil {
		return nil, err
	}
	if cfg.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported schema_version %d", cfg.SchemaVersion)
	}
	cfg.Name, err = requiredString(raw.top, "name")
	if err != nil {
		return nil, err
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("name must not be empty")
	}

	if command == CommandServer {
		if cfg.Server.Listen, err = requiredString(raw.server, "listen"); err != nil {
			return nil, err
		}
		if cfg.Server.HTTPPath, err = optionalString(raw.server, "http_path", defaultServerHTTPPath); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(cfg.Server.HTTPPath, "/") {
			return nil, fmt.Errorf("server.http_path must start with /")
		}
		if cfg.Server.Connect, err = requiredString(raw.server, "connect"); err != nil {
			return nil, err
		}
	}
	if command == CommandProxy || command == CommandServer {
		if cfg.Resume.Timeout, err = optionalDuration(raw.resume, "timeout", defaultResumeTimeout); err != nil {
			return nil, err
		}
		if cfg.Resume.BufferBytes, err = optionalInt(raw.resume, "buffer_bytes", defaultResumeBufferBytes); err != nil {
			return nil, err
		}
		if cfg.Resume.Timeout <= 0 || cfg.Resume.BufferBytes <= 0 {
			return nil, fmt.Errorf("resume timeout and buffer_bytes must be positive")
		}
	}
	if command == CommandProxy || command == CommandProbe {
		if cfg.Probe.Interval, err = optionalDuration(raw.probe, "interval", defaultProbeInterval); err != nil {
			return nil, err
		}
		if cfg.Probe.Timeout, err = optionalDuration(raw.probe, "timeout", defaultProbeTimeout); err != nil {
			return nil, err
		}
		if cfg.Probe.SwitchCooldown, err = optionalDuration(raw.probe, "switch_cooldown", defaultProbeSwitchCooldown); err != nil {
			return nil, err
		}
		if cfg.Probe.ActiveFailureThreshold, err = optionalInt(raw.probe, "active_failure_threshold", defaultProbeActiveFailureThreshold); err != nil {
			return nil, err
		}
		if cfg.Probe.CandidateSuccessThreshold, err = optionalInt(raw.probe, "candidate_success_threshold", defaultProbeCandidateSuccessThreshold); err != nil {
			return nil, err
		}
		if cfg.Probe.BetterRTTMinDelta, err = optionalDuration(raw.probe, "better_rtt_min_delta", defaultProbeBetterRTTMinDelta); err != nil {
			return nil, err
		}
		if cfg.Probe.BetterRTTRatio, err = optionalFloat(raw.probe, "better_rtt_ratio", defaultProbeBetterRTTRatio); err != nil {
			return nil, err
		}
		if cfg.Probe.Interval <= 0 || cfg.Probe.Timeout <= 0 || cfg.Probe.SwitchCooldown < 0 ||
			cfg.Probe.ActiveFailureThreshold <= 0 || cfg.Probe.CandidateSuccessThreshold <= 0 ||
			cfg.Probe.BetterRTTMinDelta < 0 || cfg.Probe.BetterRTTRatio < 0 {
			return nil, fmt.Errorf("probe fields must be positive")
		}
		cfg.Paths, err = parsePaths(raw.paths)
		if err != nil {
			return nil, err
		}
		if len(enabledPaths(cfg.Paths)) == 0 {
			return nil, fmt.Errorf("at least one enabled path is required")
		}
	}
	return cfg, nil
}

func parsePaths(raw []map[string]any) ([]PathConfig, error) {
	seen := map[string]bool{}
	var paths []PathConfig
	for i, item := range raw {
		var err error
		p := PathConfig{}
		if p.Name, err = requiredString(item, "name"); err != nil {
			return nil, fmt.Errorf("paths[%d].%w", i, err)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("duplicate path name %q", p.Name)
		}
		seen[p.Name] = true
		if p.Transport, err = optionalString(item, "transport", defaultPathTransport); err != nil {
			return nil, fmt.Errorf("paths[%d].%w", i, err)
		}
		if p.Transport != "ws" {
			return nil, fmt.Errorf("paths[%d].transport must be ws", i)
		}
		if p.Endpoint, err = requiredString(item, "endpoint"); err != nil {
			return nil, fmt.Errorf("paths[%d].%w", i, err)
		}
		u, err := url.Parse(p.Endpoint)
		if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
			return nil, fmt.Errorf("paths[%d].endpoint must be ws:// or wss:// URL", i)
		}
		if p.Priority, err = optionalInt(item, "priority", defaultPathPriority); err != nil {
			return nil, fmt.Errorf("paths[%d].%w", i, err)
		}
		if p.Enabled, err = optionalBool(item, "enabled", defaultPathEnabled); err != nil {
			return nil, fmt.Errorf("paths[%d].%w", i, err)
		}
		paths = append(paths, p)
	}
	return paths, nil
}

func enabledPaths(paths []PathConfig) []PathConfig {
	var enabled []PathConfig
	for _, p := range paths {
		if p.Enabled {
			enabled = append(enabled, p)
		}
	}
	return enabled
}
