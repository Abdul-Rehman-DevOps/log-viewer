package config_test

import (
	"testing"

	"github.com/AIVMNetwork/log-viewer/internal/config"
)

func TestResolveNamespacesExclude(t *testing.T) {
	cfg := config.Settings{
		Mode:    "exclude",
		Exclude: []string{"kube-system", "kube-public"},
	}
	all := []string{"default", "kube-system", "app", "kube-public"}
	got := config.ResolveNamespaces(cfg, all)
	if len(got) != 2 || got[0] != "default" || got[1] != "app" {
		t.Fatalf("unexpected: %#v", got)
	}
}

func TestResolveNamespacesInclude(t *testing.T) {
	cfg := config.Settings{
		Mode:    "include",
		Include: []string{"app", "staging"},
	}
	all := []string{"default", "app", "staging", "kube-system"}
	got := config.ResolveNamespaces(cfg, all)
	if len(got) != 2 {
		t.Fatalf("unexpected: %#v", got)
	}
}
