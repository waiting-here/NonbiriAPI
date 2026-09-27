package config

import (
	"strings"
	"testing"
	"time"
)

func setTimeoutEnvs(t *testing.T, startup, shutdown string) {
	t.Helper()
	t.Setenv("NONBIRI_STARTUP_TIMEOUT_SECONDS", startup)
	t.Setenv("NONBIRI_SHUTDOWN_TIMEOUT_SECONDS", shutdown)
}

func TestLoadTimeoutsDefaultsAndConfigFields(t *testing.T) {
	setTimeoutEnvs(t, "", " ")

	startup, shutdown, err := LoadTimeouts()
	if err != nil {
		t.Fatalf("LoadTimeouts: %v", err)
	}
	if startup != 300*time.Second || shutdown != 30*time.Second {
		t.Fatalf("LoadTimeouts = (%s, %s), want (5m0s, 30s)", startup, shutdown)
	}

	allEnvs(t)
	setTimeoutEnvs(t, "", "")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	closeLoadedVault(t, c)
	if c.StartupTimeout != startup || c.ShutdownTimeout != shutdown {
		t.Fatalf("Config timeouts = (%s, %s), want (%s, %s)", c.StartupTimeout, c.ShutdownTimeout, startup, shutdown)
	}

	setTimeoutEnvs(t, "75", "11")
	c, err = Load()
	if err != nil {
		t.Fatalf("Load with explicit timeouts: %v", err)
	}
	closeLoadedVault(t, c)
	if c.StartupTimeout != 75*time.Second || c.ShutdownTimeout != 11*time.Second {
		t.Fatalf("Config timeouts = (%s, %s), want (1m15s, 11s)", c.StartupTimeout, c.ShutdownTimeout)
	}
}

func TestLoadTimeoutsAcceptInclusiveBounds(t *testing.T) {
	tests := []struct {
		name              string
		startup, shutdown string
		wantStartup       time.Duration
		wantShutdown      time.Duration
	}{
		{
			name:    "minimum",
			startup: "30", shutdown: "5",
			wantStartup: 30 * time.Second, wantShutdown: 5 * time.Second,
		},
		{
			name:    "maximum",
			startup: "1800", shutdown: "120",
			wantStartup: 30 * time.Minute, wantShutdown: 2 * time.Minute,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setTimeoutEnvs(t, tt.startup, tt.shutdown)
			startup, shutdown, err := LoadTimeouts()
			if err != nil {
				t.Fatalf("LoadTimeouts: %v", err)
			}
			if startup != tt.wantStartup || shutdown != tt.wantShutdown {
				t.Fatalf("LoadTimeouts = (%s, %s), want (%s, %s)", startup, shutdown, tt.wantStartup, tt.wantShutdown)
			}
		})
	}
}

func TestLoadTimeoutsRejectOutOfRangeAndNonIntegerValues(t *testing.T) {
	tests := []struct {
		name  string
		env   string
		value string
	}{
		{name: "startup below minimum", env: "NONBIRI_STARTUP_TIMEOUT_SECONDS", value: "29"},
		{name: "startup above maximum", env: "NONBIRI_STARTUP_TIMEOUT_SECONDS", value: "1801"},
		{name: "startup fractional", env: "NONBIRI_STARTUP_TIMEOUT_SECONDS", value: "30.5"},
		{name: "startup overflow", env: "NONBIRI_STARTUP_TIMEOUT_SECONDS", value: "99999999999999999999"},
		{name: "shutdown below minimum", env: "NONBIRI_SHUTDOWN_TIMEOUT_SECONDS", value: "4"},
		{name: "shutdown above maximum", env: "NONBIRI_SHUTDOWN_TIMEOUT_SECONDS", value: "121"},
		{name: "shutdown non-integer", env: "NONBIRI_SHUTDOWN_TIMEOUT_SECONDS", value: "ten"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setTimeoutEnvs(t, "300", "30")
			t.Setenv(tt.env, tt.value)
			_, _, err := LoadTimeouts()
			if err == nil {
				t.Fatalf("LoadTimeouts accepted %s=%q", tt.env, tt.value)
			}
			if !strings.Contains(err.Error(), tt.env) {
				t.Fatalf("LoadTimeouts error %q does not identify %s", err, tt.env)
			}
			if strings.Contains(err.Error(), tt.value) {
				t.Fatalf("LoadTimeouts error echoed the supplied value: %q", err)
			}
		})
	}
}

func TestLoadAggregatesTimeoutAndExistingConfigErrorsSafely(t *testing.T) {
	allEnvs(t)
	setTimeoutEnvs(t, "29", "not-a-duration")
	t.Setenv("NONBIRI_ADMIN_PASSWORD", "config-secret-sentinel")
	t.Setenv("NONBIRI_SMTP_HOST", "mail.example.test")
	t.Setenv("NONBIRI_SMTP_PORT", "99999")

	c, err := Load()
	if err == nil {
		t.Fatal("Load accepted invalid startup, shutdown, and SMTP settings")
	}
	if c != nil {
		t.Fatal("Load returned a Config with invalid settings")
	}
	for _, field := range []string{
		"NONBIRI_STARTUP_TIMEOUT_SECONDS",
		"NONBIRI_SHUTDOWN_TIMEOUT_SECONDS",
		"NONBIRI_SMTP_PORT",
	} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("Load error %q does not include %s", err, field)
		}
	}
	if strings.Contains(err.Error(), "not-a-duration") || strings.Contains(err.Error(), "config-secret-sentinel") {
		t.Fatalf("Load error exposed a raw value: %q", err)
	}
}
