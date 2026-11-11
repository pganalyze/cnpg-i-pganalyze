package config

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func mustPlugin(t *testing.T, apiKey string) *Plugin {
	t.Helper()
	plugin, err := NewPlugin("", apiKey, "", ResourceSettings{})
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	return plugin
}

func TestNewPluginDefaults(t *testing.T) {
	plugin := mustPlugin(t, "key")

	if plugin.CollectorImage != DefaultCollectorImage {
		t.Errorf("image = %q", plugin.CollectorImage)
	}
	if got := plugin.SidecarResources.Requests[corev1.ResourceMemory]; got.String() != "256Mi" {
		t.Errorf("memory request = %s", got.String())
	}
	if _, ok := plugin.SidecarResources.Limits[corev1.ResourceCPU]; ok {
		t.Errorf("expected no CPU limit by default")
	}
}

func TestNewPluginRejectsBadQuantity(t *testing.T) {
	if _, err := NewPlugin("", "key", "", ResourceSettings{MemoryLimit: "lots"}); err == nil {
		t.Fatal("expected an error for an invalid quantity")
	}
}

func TestParseClusterDefaults(t *testing.T) {
	settings, errs := ParseCluster(mustPlugin(t, "key"), nil)

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !settings.Logs || !settings.AutoSetup {
		t.Errorf("logs and autoSetup should default to on: %+v", settings)
	}
	if settings.Databases != DefaultDatabases || settings.Image != DefaultCollectorImage {
		t.Errorf("unexpected defaults: %+v", settings)
	}
}

func TestParseClusterOverrides(t *testing.T) {
	settings, errs := ParseCluster(mustPlugin(t, ""), map[string]string{
		ParamAPIKeySecretName: "my-key",
		ParamAPIBaseURL:       "https://pganalyze.internal",
		ParamDatabases:        "app",
		ParamLogs:             "false",
		ParamAutoSetup:        "false",
		ParamImage:            "example.com/collector:v1",
	})

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if settings.APIKeySecretName != "my-key" || settings.APIKeySecretKey != DefaultAPIKeySecretKey {
		t.Errorf("unexpected API key settings: %+v", settings)
	}
	if settings.Logs || settings.AutoSetup {
		t.Errorf("logs and autoSetup should be off: %+v", settings)
	}
	if settings.APIBaseURL != "https://pganalyze.internal" || settings.Databases != "app" ||
		settings.Image != "example.com/collector:v1" {
		t.Errorf("unexpected overrides: %+v", settings)
	}
}

func TestParseClusterErrors(t *testing.T) {
	tests := map[string]struct {
		apiKey     string
		parameters map[string]string
		wantParam  string
	}{
		"no API key anywhere": {"", nil, ParamAPIKeySecretName},
		"unknown parameter":   {"key", map[string]string{"logz": "true"}, "logz"},
		"bad boolean":         {"key", map[string]string{ParamLogs: "yes please"}, ParamLogs},
		"bad base URL":        {"key", map[string]string{ParamAPIBaseURL: "pganalyze.internal"}, ParamAPIBaseURL},
		"empty databases":     {"key", map[string]string{ParamDatabases: " "}, ParamDatabases},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, errs := ParseCluster(mustPlugin(t, tc.apiKey), tc.parameters)
			if len(errs) != 1 || errs[0].Name != tc.wantParam {
				t.Fatalf("errors = %v, want one for %q", errs, tc.wantParam)
			}
		})
	}
}
