// Package config holds the plugin-wide settings (read from the plugin
// Deployment's environment) and the per-cluster parameters (read from the
// plugin entry in the Cluster's spec.plugins list).
package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// DefaultCollectorImage is the collector image injected as a sidecar
	// unless overridden plugin-wide or per cluster
	DefaultCollectorImage = "quay.io/pganalyze/collector:stable"

	// DefaultAPIKeySecretKey is the key read from a per-cluster API key Secret
	DefaultAPIKeySecretKey = "PGA_API_KEY" // #nosec G101 -- name of a key, not a credential

	// DefaultDatabases monitors every database, connecting through "postgres"
	// since it exists on every cluster regardless of how it was bootstrapped
	DefaultDatabases = "postgres,*"
)

// Names of the per-cluster parameters
const (
	ParamAPIKeySecretName = "apiKeySecretName" // #nosec G101 -- name of a parameter, not a credential
	ParamAPIKeySecretKey  = "apiKeySecretKey"  // #nosec G101 -- name of a parameter, not a credential
	ParamAPIBaseURL       = "apiBaseUrl"
	ParamDatabases        = "databases"
	ParamLogs             = "logs"
	ParamAutoSetup        = "autoSetup"
	ParamImage            = "image"
)

// Plugin holds the plugin-wide settings
type Plugin struct {
	// CollectorImage is the default collector image for the sidecar
	CollectorImage string
	// DefaultAPIKey is used for clusters that don't name their own Secret.
	// It is passed to the sidecar as a plain environment value.
	DefaultAPIKey string
	// DefaultAPIBaseURL is used for clusters that don't set apiBaseUrl
	DefaultAPIBaseURL string
	// SidecarResources are the resource requirements of the sidecar
	SidecarResources corev1.ResourceRequirements
}

// ResourceSettings are the textual resource settings of the sidecar, as
// read from the environment. Empty values fall back to the defaults, and
// an empty limit default means no limit.
type ResourceSettings struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

const (
	defaultCPURequest    = "50m"
	defaultMemoryRequest = "256Mi"
	defaultMemoryLimit   = "1Gi"
)

// NewPlugin builds the plugin-wide settings, applying defaults
func NewPlugin(collectorImage, defaultAPIKey, defaultAPIBaseURL string, res ResourceSettings) (*Plugin, error) {
	if collectorImage == "" {
		collectorImage = DefaultCollectorImage
	}

	requirements := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	for _, item := range []struct {
		list     corev1.ResourceList
		name     corev1.ResourceName
		value    string
		fallback string
	}{
		{requirements.Requests, corev1.ResourceCPU, res.CPURequest, defaultCPURequest},
		{requirements.Requests, corev1.ResourceMemory, res.MemoryRequest, defaultMemoryRequest},
		{requirements.Limits, corev1.ResourceCPU, res.CPULimit, ""},
		{requirements.Limits, corev1.ResourceMemory, res.MemoryLimit, defaultMemoryLimit},
	} {
		value := item.value
		if value == "" {
			value = item.fallback
		}
		if value == "" {
			continue
		}
		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			return nil, fmt.Errorf("invalid sidecar %s quantity %q: %w", item.name, value, err)
		}
		item.list[item.name] = quantity
	}

	return &Plugin{
		CollectorImage:    collectorImage,
		DefaultAPIKey:     defaultAPIKey,
		DefaultAPIBaseURL: defaultAPIBaseURL,
		SidecarResources:  requirements,
	}, nil
}

// Cluster holds the settings of one cluster, after applying the plugin-wide
// defaults to the parameters of its plugin entry
type Cluster struct {
	// APIKeySecretName names a Secret in the cluster's namespace holding the
	// API key. When empty, the plugin-wide default API key is used.
	APIKeySecretName string
	// APIKeySecretKey is the key within that Secret
	APIKeySecretKey string
	// APIBaseURL overrides the pganalyze API endpoint (Enterprise Server)
	APIBaseURL string
	// Databases is the collector's db_name value
	Databases string
	// Logs controls whether Postgres logs are sent to the collector
	Logs bool
	// AutoSetup controls whether the sidecar ensures the monitoring role
	// and helper functions before starting the collector
	AutoSetup bool
	// Image is the collector image for the sidecar
	Image string
}

// ParameterError describes a problem with one parameter
type ParameterError struct {
	Name    string
	Message string
}

// HasAPIKey tells whether the collector can be given an API key for this
// cluster. Without one there is no point in running it.
func (c *Cluster) HasAPIKey(plugin *Plugin) bool {
	return c.APIKeySecretName != "" || plugin.DefaultAPIKey != ""
}

// ParseCluster turns the parameters of a cluster's plugin entry into its
// settings. A parameter with a problem keeps its default, so the settings
// are usable whenever HasAPIKey is true; the returned errors list every
// problem found, so they can all be reported at once.
func ParseCluster(plugin *Plugin, parameters map[string]string) (*Cluster, []ParameterError) {
	result := &Cluster{
		APIKeySecretKey: DefaultAPIKeySecretKey,
		APIBaseURL:      plugin.DefaultAPIBaseURL,
		Databases:       DefaultDatabases,
		Logs:            true,
		AutoSetup:       true,
		Image:           plugin.CollectorImage,
	}
	var errs []ParameterError

	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		value := strings.TrimSpace(parameters[name])
		switch name {
		case ParamAPIKeySecretName:
			result.APIKeySecretName = value
		case ParamAPIKeySecretKey:
			if value == "" {
				errs = append(errs, ParameterError{name, "must not be empty"})
				continue
			}
			result.APIKeySecretKey = value
		case ParamAPIBaseURL:
			if value != "" && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
				errs = append(errs, ParameterError{name, "must start with http:// or https://"})
				continue
			}
			if value != "" {
				result.APIBaseURL = value
			}
		case ParamDatabases:
			if value == "" {
				errs = append(errs, ParameterError{name, "must not be empty"})
				continue
			}
			result.Databases = value
		case ParamLogs:
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				errs = append(errs, ParameterError{name, `must be "true" or "false"`})
				continue
			}
			result.Logs = parsed
		case ParamAutoSetup:
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				errs = append(errs, ParameterError{name, `must be "true" or "false"`})
				continue
			}
			result.AutoSetup = parsed
		case ParamImage:
			if value == "" {
				errs = append(errs, ParameterError{name, "must not be empty"})
				continue
			}
			result.Image = value
		default:
			errs = append(errs, ParameterError{name, "unknown parameter"})
		}
	}

	if result.APIKeySecretName == "" && plugin.DefaultAPIKey == "" {
		errs = append(errs, ParameterError{
			ParamAPIKeySecretName,
			"is required because the plugin has no default API key configured",
		})
	}

	return result, errs
}
