// Package lifecycle implements the CNPG-I lifecycle service, which this
// plugin uses to add the pganalyze collector to every instance Pod
package lifecycle

import (
	"context"
	"errors"
	"strings"

	apiv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/object"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/machinery/pkg/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"github.com/pganalyze/cnpg-i-pganalyze/internal/config"
	"github.com/pganalyze/cnpg-i-pganalyze/pkg/metadata"
)

const (
	// SidecarName is the name of the injected collector container
	SidecarName = "pganalyze-collector"

	// MonitoringUser is the Postgres role the collector connects as. The
	// Cluster needs a "local postgres pganalyze" line in spec.postgresql.pg_ident
	// for the sidecar to be allowed to use it over the Unix socket.
	MonitoringUser = "pganalyze"

	postgresContainerName = "postgres"

	// CNPG keeps the Postgres Unix socket in the "run" directory of its
	// scratch volume, mounted at /controller in the postgres container
	scratchDataVolumeName = "scratch-data"
	socketDirectory       = "/controller/run"

	// collectorScratchVolumeName backs the collector's writable directories,
	// as its root filesystem is read-only
	collectorScratchVolumeName = "pganalyze-collector-scratch"

	// The collector receives logs on this address, and the instance manager
	// is pointed at it. The literal IPv4 address is deliberate: "localhost"
	// can resolve to ::1, where the collector doesn't listen.
	otelListenAddress   = "127.0.0.1:4318"
	otelLogsEndpoint    = "http://" + otelListenAddress + "/v1/logs"
	otelLogsEndpointEnv = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
)

// Implementation is the implementation of the lifecycle service
type Implementation struct {
	lifecycle.UnimplementedOperatorLifecycleServer
	plugin *config.Plugin
}

// NewImplementation creates the lifecycle service for the given settings
func NewImplementation(plugin *config.Plugin) *Implementation {
	return &Implementation{plugin: plugin}
}

// GetCapabilities exposes the lifecycle capabilities
func (Implementation) GetCapabilities(
	context.Context,
	*lifecycle.OperatorLifecycleCapabilitiesRequest,
) (*lifecycle.OperatorLifecycleCapabilitiesResponse, error) {
	return &lifecycle.OperatorLifecycleCapabilitiesResponse{
		LifecycleCapabilities: []*lifecycle.OperatorLifecycleCapabilities{
			{
				Group: "",
				Kind:  "Pod",
				OperationTypes: []*lifecycle.OperatorOperationType{
					{Type: lifecycle.OperatorOperationType_TYPE_CREATE},
					{Type: lifecycle.OperatorOperationType_TYPE_EVALUATE},
				},
			},
		},
	}, nil
}

// LifecycleHook is called when the operator creates or evaluates an object
func (impl Implementation) LifecycleHook(
	ctx context.Context,
	request *lifecycle.OperatorLifecycleRequest,
) (*lifecycle.OperatorLifecycleResponse, error) {
	kind, err := object.GetKind(request.GetObjectDefinition())
	if err != nil {
		return nil, err
	}
	if request.GetOperationType() == nil {
		return nil, errors.New("no operation set")
	}

	if kind != "Pod" {
		return &lifecycle.OperatorLifecycleResponse{}, nil
	}

	switch request.GetOperationType().GetType() {
	case lifecycle.OperatorOperationType_TYPE_CREATE, lifecycle.OperatorOperationType_TYPE_EVALUATE:
		return impl.reconcilePod(ctx, request)
	default:
		return &lifecycle.OperatorLifecycleResponse{}, nil
	}
}

func (impl Implementation) reconcilePod(
	ctx context.Context,
	request *lifecycle.OperatorLifecycleRequest,
) (*lifecycle.OperatorLifecycleResponse, error) {
	logger := log.FromContext(ctx).WithName("pganalyze_lifecycle")

	cluster, err := decoder.DecodeClusterLenient(request.GetClusterDefinition())
	if err != nil {
		return nil, err
	}
	pod, err := decoder.DecodePodJSON(request.GetObjectDefinition())
	if err != nil {
		return nil, err
	}
	logger = logger.WithValues("namespace", cluster.Namespace, "cluster", cluster.Name, "pod", pod.Name)

	helper := common.NewPlugin(*cluster, metadata.PluginName)
	settings, problems := config.ParseCluster(impl.plugin, helper.Parameters)
	if len(problems) > 0 {
		// A parameter with a problem keeps its default. Dropping the sidecar
		// over a typo would restart the database, so carry on and leave the
		// reporting to the Cluster status.
		logger.Info("ignoring invalid plugin parameters", "problems", problems)
	}
	if !settings.HasAPIKey(impl.plugin) {
		// Monitoring must never get in the way of the database: leave the Pod
		// untouched instead of failing its creation
		logger.Info("not injecting the collector: no API key is configured")
		return &lifecycle.OperatorLifecycleResponse{}, nil
	}

	mutatedPod := pod.DeepCopy()
	if err := impl.mutatePod(cluster, mutatedPod, settings, logger); err != nil {
		return nil, err
	}

	patch, err := object.CreatePatch(mutatedPod, pod)
	if err != nil {
		return nil, err
	}

	logger.Debug("generated patch", "content", string(patch))
	return &lifecycle.OperatorLifecycleResponse{JsonPatch: patch}, nil
}

// mutatePod adds the collector sidecar to an instance Pod and, if logs are
// enabled, points the instance manager's log export at it
func (impl Implementation) mutatePod(
	cluster *apiv1.Cluster,
	pod *corev1.Pod,
	settings *config.Cluster,
	logger log.Logger,
) error {
	logs := settings.Logs
	if logs {
		if reason := ExistingLogExport(cluster, pod); reason != "" {
			logger.Info("not configuring log export: the cluster already has one", "reason", reason)
			logs = false
		}
	}

	ensureVolume(&pod.Spec, corev1.Volume{
		Name:         collectorScratchVolumeName,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	})

	sidecar := impl.sidecarContainer(cluster, pod, settings, logs)
	// A native sidecar (an init container that keeps running) starts before
	// Postgres, so the log receiver is listening by the time the instance
	// manager starts exporting, and is stopped after Postgres on shutdown.
	if err := object.InjectPluginInitContainerSidecarSpec(&pod.Spec, sidecar, false); err != nil {
		return err
	}

	if logs {
		for i := range pod.Spec.Containers {
			if pod.Spec.Containers[i].Name == postgresContainerName && !hasEnv(pod.Spec.Containers[i], otelLogsEndpointEnv) {
				pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, corev1.EnvVar{
					Name:  otelLogsEndpointEnv,
					Value: otelLogsEndpoint,
				})
			}
		}
	}

	return nil
}

// ExistingLogExport reports why the plugin must not point the instance
// manager's log export at the collector, or "" if it is free to do so. The
// export has a single destination, so one configured by the user wins. The
// Pod is optional; without it only the Cluster is inspected.
func ExistingLogExport(cluster *apiv1.Cluster, pod *corev1.Pod) string {
	isEndpoint := func(name string) bool {
		return strings.HasPrefix(name, "OTEL_EXPORTER_OTLP_") && strings.HasSuffix(name, "ENDPOINT")
	}

	for _, env := range cluster.Spec.Env {
		if isEndpoint(env.Name) {
			return env.Name + " is set in the Cluster's env"
		}
	}
	var containers []corev1.Container
	if pod != nil {
		containers = pod.Spec.Containers
	}
	for _, container := range containers {
		if container.Name != postgresContainerName {
			continue
		}
		for _, env := range container.Env {
			if env.Name == otelLogsEndpointEnv && env.Value == otelLogsEndpoint {
				continue
			}
			if isEndpoint(env.Name) {
				return env.Name + " is set on the postgres container"
			}
		}
	}
	// Variables that come in through envFrom can't be inspected here, and an
	// explicit variable would silently override an endpoint set that way
	if len(cluster.Spec.EnvFrom) > 0 {
		return "the Cluster uses envFrom, which may already configure an endpoint"
	}

	return ""
}

func hasEnv(container corev1.Container, name string) bool {
	for _, env := range container.Env {
		if env.Name == name {
			return true
		}
	}
	return false
}

func ensureVolume(spec *corev1.PodSpec, volume corev1.Volume) {
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == volume.Name {
			return
		}
	}
	spec.Volumes = append(spec.Volumes, volume)
}

func (impl Implementation) sidecarContainer(
	cluster *apiv1.Cluster,
	pod *corev1.Pod,
	settings *config.Cluster,
	logs bool,
) *corev1.Container {
	apiKey := corev1.EnvVar{Name: "PGA_API_KEY", Value: impl.plugin.DefaultAPIKey}
	if settings.APIKeySecretName != "" {
		apiKey = corev1.EnvVar{
			Name: "PGA_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: settings.APIKeySecretName},
					Key:                  settings.APIKeySecretKey,
				},
			},
		}
	}

	env := []corev1.EnvVar{apiKey}
	if settings.APIBaseURL != "" {
		env = append(env, corev1.EnvVar{Name: "PGA_API_BASE_URL", Value: settings.APIBaseURL})
	}
	env = append(env,
		// Every instance reports as its own server. The cluster ID is what
		// pganalyze groups the instances of one cluster by.
		corev1.EnvVar{Name: "PGA_API_SYSTEM_ID", Value: pod.Name},
		corev1.EnvVar{Name: "PGA_API_SYSTEM_SCOPE", Value: cluster.Namespace + "/" + cluster.Name},
		corev1.EnvVar{Name: "API_CLUSTER_ID", Value: cluster.Namespace + "/" + cluster.Name},
		corev1.EnvVar{Name: "DB_HOST", Value: socketDirectory},
		corev1.EnvVar{Name: "DB_NAME", Value: settings.Databases},
		corev1.EnvVar{Name: "DB_USERNAME", Value: MonitoringUser},
	)
	if logs {
		env = append(env, corev1.EnvVar{Name: "LOG_OTEL_SERVER", Value: otelListenAddress})
	}

	// With no arguments the image runs the collector. The setup variant first
	// creates the monitoring role and helper functions as the postgres
	// superuser, waiting for Postgres to come up.
	var args []string
	if settings.AutoSetup {
		args = []string{"setup-and-collector"}
	}

	return &corev1.Container{
		Name:  SidecarName,
		Image: settings.Image,
		Args:  args,
		Env:   env,
		VolumeMounts: []corev1.VolumeMount{
			{Name: scratchDataVolumeName, MountPath: socketDirectory, SubPath: "run"},
			{Name: collectorScratchVolumeName, MountPath: "/state", SubPath: "state"},
			{Name: collectorScratchVolumeName, MountPath: "/config", SubPath: "config"},
			{Name: collectorScratchVolumeName, MountPath: "/tmp", SubPath: "tmp"},
		},
		Resources: impl.plugin.SidecarResources,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Privileged:               ptr.To(false),
			ReadOnlyRootFilesystem:   ptr.To(true),
			RunAsNonRoot:             ptr.To(true),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
}
