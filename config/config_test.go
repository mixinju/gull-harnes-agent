package config

import (
	"encoding/json"
	"testing"
)

func TestSandboxConfigJSON(t *testing.T) {
	var cfg Config
	raw := []byte(`{"sandbox":{"enabled":false,"bash_mode":"full","allow_network":true,"allowed_paths":["/tmp"]}}`)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Sandbox == nil || cfg.Sandbox.Enabled == nil || *cfg.Sandbox.Enabled {
		t.Fatalf("enabled = %#v", cfg.Sandbox)
	}
	if cfg.Sandbox.BashMode != "full" || cfg.Sandbox.AllowNetwork == nil || !*cfg.Sandbox.AllowNetwork {
		t.Fatalf("sandbox = %#v", cfg.Sandbox)
	}
	if len(cfg.Sandbox.AllowedPaths) != 1 || cfg.Sandbox.AllowedPaths[0] != "/tmp" {
		t.Fatalf("paths = %#v", cfg.Sandbox.AllowedPaths)
	}

	var omitted Config
	if err := json.Unmarshal([]byte(`{}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.Sandbox != nil {
		t.Fatal("omitted sandbox should be nil so defaults apply")
	}
}
