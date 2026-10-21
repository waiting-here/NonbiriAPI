package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCloudflareWorkerConfiguration(t *testing.T) {
	const token = "private-worker-token-01234567890123456789"
	const target = "https://worker.example/private-path"
	for _, tc := range []struct{ url, token string }{
		{target, ""}, {"", token}, {"http://worker.example", token}, {target + "?key=value", token}, {target + "#", token},
		{"https://user@worker.example", token}, {target, "short"}, {target, strings.Repeat("x", 4097)}, {target, token + " "}, {target, token + "\t"}, {target, token + "é"},
	} {
		if value, err := ParseCloudflareWorker(tc.url, tc.token); err == nil {
			t.Fatal("invalid configuration accepted", value)
		} else if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "private-path") {
			t.Fatal("private error", err)
		}
	}
	worker, err := ParseCloudflareWorker(target, token)
	if err != nil || worker.URL() != target || worker.Token() != token {
		t.Fatal("valid configuration failed", err)
	}
	encoded, _ := json.Marshal(worker)
	for _, output := range []string{string(encoded), fmt.Sprintf("%+v %#v", worker, worker), worker.LogValue().String()} {
		if strings.Contains(output, token) || strings.Contains(output, target) {
			t.Fatal("private configuration formatted")
		}
	}
	if empty, err := ParseCloudflareWorker("", ""); err != nil || empty.URL() != "" {
		t.Fatal(empty, err)
	}
	for _, size := range []int{32, 4096} {
		if _, err := ParseCloudflareWorker(target, strings.Repeat("x", size)); err != nil {
			t.Fatal(size, err)
		}
	}
}

func TestLoadCloudflareWorker(t *testing.T) {
	allEnvs(t)
	t.Setenv("NONBIRI_CLOUDFLARE_WORKER_URL", "https://worker.example/private")
	t.Setenv("NONBIRI_CLOUDFLARE_WORKER_TOKEN", "private-loaded-worker-token-0123456789")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	closeLoadedVault(t, cfg)
	if cfg.CloudflareWorker.URL() != "https://worker.example/private" {
		t.Fatal("Worker not loaded")
	}
	t.Setenv("NONBIRI_CLOUDFLARE_WORKER_TOKEN", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "NONBIRI_CLOUDFLARE_WORKER_URL/TOKEN") {
		t.Fatal("unpaired Worker settings accepted", err)
	}
}
