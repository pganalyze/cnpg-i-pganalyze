package operator

import (
	"strings"
	"testing"

	apiv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/pganalyze/cnpg-i-pganalyze/internal/config"
	"github.com/pganalyze/cnpg-i-pganalyze/pkg/metadata"
)

func testImplementation(t *testing.T, apiKey string) *Implementation {
	t.Helper()
	plugin, err := config.NewPlugin("", apiKey, "", config.ResourceSettings{})
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	return NewImplementation(plugin)
}

func readyCluster(parameters map[string]string) *apiv1.Cluster {
	return &apiv1.Cluster{
		Spec: apiv1.ClusterSpec{
			Plugins: []apiv1.PluginConfiguration{{Name: metadata.PluginName, Parameters: parameters}},
			PostgresConfiguration: apiv1.PostgresConfiguration{
				PgIdent:    []string{"local postgres pganalyze"},
				Parameters: map[string]string{"pg_stat_statements.track": "all"},
			},
		},
	}
}

func TestStatusReady(t *testing.T) {
	status := testImplementation(t, "key").status(readyCluster(nil))

	if status.Collector != "injected" || status.Logs != "enabled" {
		t.Errorf("unexpected status: %+v", status)
	}
	if len(status.Problems) != 0 || len(status.Warnings) != 0 {
		t.Errorf("expected no problems or warnings: %+v", status)
	}
}

func TestStatusReportsInvalidParameters(t *testing.T) {
	status := testImplementation(t, "key").status(readyCluster(map[string]string{"logz": "true"}))

	if status.Collector != "injected" {
		t.Errorf("a bad parameter must not stop the injection, collector = %q", status.Collector)
	}
	if len(status.Problems) != 1 || !strings.HasPrefix(status.Problems[0], "logz:") {
		t.Errorf("problems = %v", status.Problems)
	}
}

func TestStatusNotInjectedWithoutAPIKey(t *testing.T) {
	status := testImplementation(t, "").status(readyCluster(nil))

	if status.Collector != "not injected" || len(status.Problems) != 1 {
		t.Errorf("unexpected status: %+v", status)
	}
}

func TestStatusWarnsAboutMissingPrerequisites(t *testing.T) {
	cluster := readyCluster(nil)
	cluster.Spec.PostgresConfiguration = apiv1.PostgresConfiguration{}

	status := testImplementation(t, "key").status(cluster)

	if status.Collector != "injected" {
		t.Errorf("missing prerequisites must not stop the injection: %+v", status)
	}
	if len(status.Warnings) != 2 {
		t.Errorf("expected warnings for pg_ident and pg_stat_statements, got %v", status.Warnings)
	}
}

func TestStatusLogsNotConfiguredWithOwnExport(t *testing.T) {
	cluster := readyCluster(nil)
	cluster.Spec.Env = []corev1.EnvVar{{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: "http://otel:4318"}}

	status := testImplementation(t, "key").status(cluster)

	if !strings.HasPrefix(status.Logs, "not configured:") {
		t.Errorf("logs = %q", status.Logs)
	}
}
