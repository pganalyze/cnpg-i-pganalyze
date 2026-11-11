// Package main is the entrypoint of the pganalyze CNPG-I plugin
package main

import (
	"os"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/http"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"

	"github.com/pganalyze/cnpg-i-pganalyze/internal/config"
	"github.com/pganalyze/cnpg-i-pganalyze/internal/identity"
	lifecycleImpl "github.com/pganalyze/cnpg-i-pganalyze/internal/lifecycle"
	operatorImpl "github.com/pganalyze/cnpg-i-pganalyze/internal/operator"
)

func main() {
	cobra.EnableTraverseRunHooks = true

	logFlags := &log.Flags{}
	rootCmd := &cobra.Command{
		Use:   "cnpg-i-pganalyze",
		Short: "A CloudNativePG plugin that monitors clusters with pganalyze",
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			logFlags.ConfigureLogging()
			cmd.SetContext(log.IntoContext(cmd.Context(), log.GetLogger()))
		},
	}

	for key, env := range map[string]string{
		"collector-image":        "COLLECTOR_IMAGE",
		"api-key":                "PGA_API_KEY",
		"api-base-url":           "PGA_API_BASE_URL",
		"sidecar-cpu-request":    "SIDECAR_CPU_REQUEST",
		"sidecar-cpu-limit":      "SIDECAR_CPU_LIMIT",
		"sidecar-memory-request": "SIDECAR_MEMORY_REQUEST",
		"sidecar-memory-limit":   "SIDECAR_MEMORY_LIMIT",
	} {
		_ = viper.BindEnv(key, env)
	}

	logFlags.AddFlags(rootCmd.PersistentFlags())
	rootCmd.AddCommand(newPluginCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newPluginCmd() *cobra.Command {
	cmd := http.CreateMainCmd(identity.Implementation{}, func(server *grpc.Server) error {
		// Read the settings here, not in main, so that viper has the environment loaded
		plugin, err := config.NewPlugin(
			viper.GetString("collector-image"),
			viper.GetString("api-key"),
			viper.GetString("api-base-url"),
			config.ResourceSettings{
				CPURequest:    viper.GetString("sidecar-cpu-request"),
				CPULimit:      viper.GetString("sidecar-cpu-limit"),
				MemoryRequest: viper.GetString("sidecar-memory-request"),
				MemoryLimit:   viper.GetString("sidecar-memory-limit"),
			},
		)
		if err != nil {
			return err
		}

		operator.RegisterOperatorServer(server, operatorImpl.NewImplementation(plugin))
		lifecycle.RegisterOperatorLifecycleServer(server, lifecycleImpl.NewImplementation(plugin))
		return nil
	})
	cmd.Use = "plugin"

	return cmd
}
