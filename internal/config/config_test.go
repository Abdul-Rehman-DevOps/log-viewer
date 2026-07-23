package config_test

import (
	"testing"

	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/config"
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

func TestWorkloadAllowedEmptyMeansAll(t *testing.T) {
	cfg := config.Settings{AllowedWorkloads: nil}
	if !config.WorkloadAllowed(cfg, "dev", "Deployment", "api") {
		t.Fatal("empty allowlist should allow all")
	}
}

func TestWorkloadAllowedPins(t *testing.T) {
	cfg := config.Settings{
		AllowedWorkloads: []string{"dev/Deployment/aivm-agents-dev", "staging/Deployment/aivm-agents-staging"},
	}
	if !config.WorkloadAllowed(cfg, "dev", "Deployment", "aivm-agents-dev") {
		t.Fatal("expected pin match")
	}
	if config.WorkloadAllowed(cfg, "argocd", "Deployment", "argocd-server") {
		t.Fatal("unpinned app must be blocked")
	}
	// case-insensitive kind
	if !config.WorkloadAllowed(cfg, "dev", "deployment", "aivm-agents-dev") {
		t.Fatal("expected case-insensitive kind match")
	}
}

func TestFilterNamespacesForScan(t *testing.T) {
	cfg := config.Settings{
		AllowedWorkloads: []string{"dev/Deployment/a", "staging/Deployment/b"},
	}
	resolved := []string{"dev", "staging", "argocd", "kube-system"}
	got := config.FilterNamespacesForScan(cfg, resolved)
	if len(got) != 2 {
		t.Fatalf("expected 2 pin namespaces, got %#v", got)
	}
	cfg2 := config.Settings{AllowedWorkloads: nil}
	got2 := config.FilterNamespacesForScan(cfg2, resolved)
	if len(got2) != 4 {
		t.Fatalf("empty pins should keep all resolved: %#v", got2)
	}
}
