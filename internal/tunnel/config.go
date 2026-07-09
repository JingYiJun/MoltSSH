package tunnel

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	CommandProxy  = "proxy"
	CommandServer = "server"
	CommandProbe  = "probe"
)

type Config struct {
	SchemaVersion int
	Name          string
	Server        ServerConfig
	Resume        ResumeConfig
	Probe         ProbeConfig
	Paths         []PathConfig
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

type rawConfig struct {
	top    map[string]any
	server map[string]any
	resume map[string]any
	probe  map[string]any
	paths  []map[string]any
}

func LoadConfigFile(path, command string) (*Config, error) {
	if path == "" {
		return nil, fmt.Errorf("--config is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseConfig(string(data), command)
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
		if cfg.Server.HTTPPath, err = requiredString(raw.server, "http_path"); err != nil {
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
		if cfg.Resume.Timeout, err = requiredDuration(raw.resume, "timeout"); err != nil {
			return nil, err
		}
		if cfg.Resume.BufferBytes, err = requiredInt(raw.resume, "buffer_bytes"); err != nil {
			return nil, err
		}
		if cfg.Resume.Timeout <= 0 || cfg.Resume.BufferBytes <= 0 {
			return nil, fmt.Errorf("resume timeout and buffer_bytes must be positive")
		}
	}
	if command == CommandProxy || command == CommandProbe {
		if cfg.Probe.Interval, err = requiredDuration(raw.probe, "interval"); err != nil {
			return nil, err
		}
		if cfg.Probe.Timeout, err = requiredDuration(raw.probe, "timeout"); err != nil {
			return nil, err
		}
		if cfg.Probe.SwitchCooldown, err = requiredDuration(raw.probe, "switch_cooldown"); err != nil {
			return nil, err
		}
		if cfg.Probe.ActiveFailureThreshold, err = requiredInt(raw.probe, "active_failure_threshold"); err != nil {
			return nil, err
		}
		if cfg.Probe.CandidateSuccessThreshold, err = requiredInt(raw.probe, "candidate_success_threshold"); err != nil {
			return nil, err
		}
		if cfg.Probe.BetterRTTMinDelta, err = requiredDuration(raw.probe, "better_rtt_min_delta"); err != nil {
			return nil, err
		}
		if cfg.Probe.BetterRTTRatio, err = requiredFloat(raw.probe, "better_rtt_ratio"); err != nil {
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
		if p.Transport, err = requiredString(item, "transport"); err != nil {
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
		if p.Priority, err = requiredInt(item, "priority"); err != nil {
			return nil, fmt.Errorf("paths[%d].%w", i, err)
		}
		if p.Enabled, err = requiredBool(item, "enabled"); err != nil {
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

func parseTOML(data string) (*rawConfig, error) {
	raw := &rawConfig{
		top:    map[string]any{},
		server: map[string]any{},
		resume: map[string]any{},
		probe:  map[string]any{},
	}
	current := raw.top
	section := "top"
	for i, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(stripComment(line))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "[["), "]]"))
			if name != "paths" {
				return nil, fmt.Errorf("line %d: unknown table array %q", i+1, name)
			}
			raw.paths = append(raw.paths, map[string]any{})
			current = raw.paths[len(raw.paths)-1]
			section = "paths"
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			switch name {
			case "server":
				current = raw.server
			case "resume":
				current = raw.resume
			case "probe":
				current = raw.probe
			default:
				return nil, fmt.Errorf("line %d: unknown section %q", i+1, name)
			}
			section = name
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", i+1)
		}
		key = strings.TrimSpace(key)
		if !allowedKey(section, key) {
			return nil, fmt.Errorf("line %d: unknown key %s.%s", i+1, section, key)
		}
		if _, exists := current[key]; exists {
			return nil, fmt.Errorf("line %d: duplicate key %s.%s", i+1, section, key)
		}
		parsed, err := parseValue(strings.TrimSpace(val))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		current[key] = parsed
	}
	return raw, nil
}

func stripComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return line[:i]
		}
	}
	return line
}

func allowedKey(section, key string) bool {
	keys := map[string]map[string]bool{
		"top": {
			"schema_version": true, "name": true,
		},
		"server": {
			"listen": true, "http_path": true, "connect": true,
		},
		"resume": {
			"timeout": true, "buffer_bytes": true,
		},
		"probe": {
			"interval": true, "timeout": true, "switch_cooldown": true,
			"active_failure_threshold": true, "candidate_success_threshold": true,
			"better_rtt_min_delta": true, "better_rtt_ratio": true,
		},
		"paths": {
			"name": true, "transport": true, "endpoint": true, "priority": true, "enabled": true,
		},
	}
	return keys[section][key]
}

func parseValue(s string) (any, error) {
	if strings.HasPrefix(s, "\"") {
		v, err := strconv.Unquote(s)
		if err != nil {
			return nil, err
		}
		return v, nil
	}
	switch s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if strings.Contains(s, ".") {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("bad float %q", s)
		}
		return v, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("bad value %q", s)
	}
	return v, nil
}

func requiredString(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", fmt.Errorf("missing %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return s, nil
}

func requiredDuration(m map[string]any, key string) (time.Duration, error) {
	s, err := requiredString(m, key)
	if err != nil {
		return 0, err
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration", key)
	}
	return d, nil
}

func requiredInt(m map[string]any, key string) (int, error) {
	v, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("missing %s", key)
	}
	n, ok := v.(int)
	if !ok {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return n, nil
}

func requiredFloat(m map[string]any, key string) (float64, error) {
	v, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("missing %s", key)
	}
	switch n := v.(type) {
	case int:
		return float64(n), nil
	case float64:
		return n, nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}

func requiredBool(m map[string]any, key string) (bool, error) {
	v, ok := m[key]
	if !ok {
		return false, fmt.Errorf("missing %s", key)
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return b, nil
}
