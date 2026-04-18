package envutil

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

func String(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func Int(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid int env", "key", key, "value", v, "default", def)
		return def
	}
	return n
}

func Duration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Warn("invalid duration env", "key", key, "value", v, "default", def)
		return def
	}
	return d
}

func StringSlice(key, def string) []string {
	raw := String(key, def)
	if raw == "" {
		return nil
	}
	parts := []string{}
	start := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] == ',' {
			s := trim(raw[start:i])
			if s != "" {
				parts = append(parts, s)
			}
			start = i + 1
		}
	}
	s := trim(raw[start:])
	if s != "" {
		parts = append(parts, s)
	}
	return parts
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
