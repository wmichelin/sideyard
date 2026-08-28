package main

import (
	"bufio"
	"os"
	"strings"
)

// loadEnvFile sets env vars from path for keys not already set (local dev convenience).
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

func devRoutesEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("ENABLE_DEV_ROUTES")))
	return v == "1" || v == "true" || v == "yes"
}
