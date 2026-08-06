package tunnel

import (
	"fmt"
	"strings"
)

type rawConfig struct {
	top    map[string]any
	server map[string]any
	resume map[string]any
	probe  map[string]any
	paths  []map[string]any
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
