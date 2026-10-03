package gatewayconfig

import (
	"os"
	"strings"
	"testing"
	"time"
)

// loadSessionIdle loads the document as a listener of kind, with
// listener.session_idle set to value unless value is empty.
func loadSessionIdle(t *testing.T, kind, value string) (*Config, error) {
	t.Helper()
	path := write(t, "")
	if kind == "stdio" {
		path = writeDocument(t, without(t, "  address: 127.0.0.1:8080"))
	}
	setEnv(t, "listener.kind", kind)
	if value != "" {
		setEnv(t, "listener.session_idle", value)
	}
	return Load(path, os.Environ())
}

// TestSessionIdleIsBounded: a stateful listener stands at thirty minutes when
// silent, the empty value here, and takes an idle bound from one minute to a day, at each bound and
// not one step past it.
func TestSessionIdleIsBounded(t *testing.T) {
	for value, want := range map[string]time.Duration{"": 30 * time.Minute, "1m": time.Minute, "24h": 24 * time.Hour, "2h": 2 * time.Hour} {
		cfg, err := loadSessionIdle(t, "stateful_http", value)
		if err != nil {
			t.Fatalf("%q was refused: %v", value, err)
		}
		if got := cfg.Listener.SessionIdle; got != want {
			t.Errorf("%q reads as %v, want %v", value, got, want)
		}
	}
	for _, value := range []string{"59s", "24h0m1s", "0s", "-5m"} {
		_, err := loadSessionIdle(t, "stateful_http", value)
		if err == nil || !strings.Contains(err.Error(), "listener.session_idle") {
			t.Errorf("%s: %v, want a refusal naming the key", value, err)
		}
	}
}

// TestSessionIdleNeedsAStatefulListener: a listener that keeps no session
// takes no idle bound, and the refusal names the key and the kind.
func TestSessionIdleNeedsAStatefulListener(t *testing.T) {
	for _, kind := range []string{"stateless_http", "stdio"} {
		if _, err := loadSessionIdle(t, kind, ""); err != nil {
			t.Fatalf("a %s listener silent on session_idle was refused: %v", kind, err)
		}
		_, err := loadSessionIdle(t, kind, "30m")
		if err == nil || !strings.Contains(err.Error(), "listener.session_idle") || !strings.Contains(err.Error(), kind) {
			t.Errorf("session_idle on a %s listener: %v, want a refusal naming the key and the kind", kind, err)
		}
	}
}

// loadMaxSessions loads the document as a listener of kind, with
// listener.max_sessions set to value unless value is empty.
func loadMaxSessions(t *testing.T, kind, value string) (*Config, error) {
	t.Helper()
	path := write(t, "")
	if kind == "stdio" {
		path = writeDocument(t, without(t, "  address: 127.0.0.1:8080"))
	}
	setEnv(t, "listener.kind", kind)
	if value != "" {
		setEnv(t, "listener.max_sessions", value)
	}
	return Load(path, os.Environ())
}

// TestMaxSessionsIsBounded: a stateful listener caps its live sessions at
// 1024 when silent, the empty value here, and takes a cap from one to 16384,
// at each bound and not one step past it.
func TestMaxSessionsIsBounded(t *testing.T) {
	for value, want := range map[string]int{"": 1024, "1": 1, "16384": 16384, "64": 64} {
		cfg, err := loadMaxSessions(t, "stateful_http", value)
		if err != nil {
			t.Fatalf("%q was refused: %v", value, err)
		}
		if got := cfg.Listener.MaxSessions; got != want {
			t.Errorf("%q reads as %d, want %d", value, got, want)
		}
	}
	for _, value := range []string{"0", "-1", "16385", "many"} {
		_, err := loadMaxSessions(t, "stateful_http", value)
		if err == nil || !strings.Contains(err.Error(), "listener.max_sessions") {
			t.Errorf("%s: %v, want a refusal naming the key", value, err)
		}
	}
}

// TestMaxSessionsNeedsAStatefulListener: a listener that keeps no session
// takes no cap on them, and the refusal names the key and the kind.
func TestMaxSessionsNeedsAStatefulListener(t *testing.T) {
	for _, kind := range []string{"stateless_http", "stdio"} {
		if _, err := loadMaxSessions(t, kind, ""); err != nil {
			t.Fatalf("a %s listener silent on max_sessions was refused: %v", kind, err)
		}
		_, err := loadMaxSessions(t, kind, "8")
		if err == nil || !strings.Contains(err.Error(), "listener.max_sessions") || !strings.Contains(err.Error(), kind) {
			t.Errorf("max_sessions on a %s listener: %v, want a refusal naming the key and the kind", kind, err)
		}
	}
}
