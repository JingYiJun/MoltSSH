package tunnel

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

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

func optionalString(m map[string]any, key, def string) (string, error) {
	if _, ok := m[key]; !ok {
		return def, nil
	}
	return requiredString(m, key)
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

func optionalDuration(m map[string]any, key string, def time.Duration) (time.Duration, error) {
	if _, ok := m[key]; !ok {
		return def, nil
	}
	return requiredDuration(m, key)
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

func optionalInt(m map[string]any, key string, def int) (int, error) {
	if _, ok := m[key]; !ok {
		return def, nil
	}
	return requiredInt(m, key)
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

func optionalFloat(m map[string]any, key string, def float64) (float64, error) {
	if _, ok := m[key]; !ok {
		return def, nil
	}
	return requiredFloat(m, key)
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

func optionalBool(m map[string]any, key string, def bool) (bool, error) {
	if _, ok := m[key]; !ok {
		return def, nil
	}
	return requiredBool(m, key)
}
