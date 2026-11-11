package operator

import (
	"context"
	"strings"

	apiv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/clusterstatus"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"

	"github.com/pganalyze/cnpg-i-pganalyze/internal/config"
	"github.com/pganalyze/cnpg-i-pganalyze/internal/lifecycle"
	"github.com/pganalyze/cnpg-i-pganalyze/pkg/metadata"
)

// Status is what the plugin reports under its entry in the Cluster's
// status.pluginStatus
type Status struct {
	// Collector tells whether the collector sidecar is added to instance Pods
	Collector string `json:"collector"`
	// Logs tells whether Postgres logs are sent to the collector
	Logs string `json:"logs"`
	// Problems are invalid plugin parameters. They keep their defaults, and
	// only a missing API key stops the collector from being injected.
	Problems []string `json:"problems,omitempty"`
	// Warnings are missing prerequisites that limit what gets collected. The
	// plugin never changes the Cluster to fix them.
	Warnings []string `json:"warnings,omitempty"`
}

// SetStatusInCluster reports the plugin's state for a Cluster
func (impl Implementation) SetStatusInCluster(
	_ context.Context,
	request *operator.SetStatusInClusterRequest,
) (*operator.SetStatusInClusterResponse, error) {
	cluster, err := decoder.DecodeClusterLenient(request.GetCluster())
	if err != nil {
		return nil, err
	}

	return clusterstatus.NewSetStatusInClusterResponseBuilder().JSONStatusResponse(impl.status(cluster))
}

func (impl Implementation) status(cluster *apiv1.Cluster) Status {
	helper := common.NewPlugin(*cluster, metadata.PluginName)
	settings, problems := config.ParseCluster(impl.plugin, helper.Parameters)

	status := Status{Collector: "injected", Logs: "enabled"}
	for _, problem := range problems {
		status.Problems = append(status.Problems, problem.Name+": "+problem.Message)
	}
	if !settings.HasAPIKey(impl.plugin) {
		status.Collector = "not injected"
		status.Logs = "disabled"
		return status
	}

	if !settings.Logs {
		status.Logs = "disabled"
	} else if reason := lifecycle.ExistingLogExport(cluster, nil); reason != "" {
		status.Logs = "not configured: " + reason
	}

	postgres := cluster.Spec.PostgresConfiguration
	if !hasIdentMapping(postgres.PgIdent) {
		status.Warnings = append(status.Warnings,
			`spec.postgresql.pg_ident needs the line "local postgres `+lifecycle.MonitoringUser+
				`" for the collector to connect`)
	}
	if !hasStatStatements(postgres) {
		status.Warnings = append(status.Warnings,
			"pg_stat_statements is not enabled: set a pg_stat_statements.* parameter in "+
				"spec.postgresql.parameters to collect query statistics")
	}

	return status
}

// hasIdentMapping tells whether pg_ident lets the pod's OS user connect as
// the monitoring role through CNPG's "local" map
func hasIdentMapping(lines []string) bool {
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "local" && fields[2] == lifecycle.MonitoringUser {
			return true
		}
	}
	return false
}

// hasStatStatements mirrors how CNPG decides to preload pg_stat_statements:
// it does so when any pg_stat_statements.* parameter is set
func hasStatStatements(postgres apiv1.PostgresConfiguration) bool {
	for name := range postgres.Parameters {
		if strings.HasPrefix(name, "pg_stat_statements.") {
			return true
		}
	}
	for _, library := range postgres.AdditionalLibraries {
		if library == "pg_stat_statements" {
			return true
		}
	}
	return false
}
