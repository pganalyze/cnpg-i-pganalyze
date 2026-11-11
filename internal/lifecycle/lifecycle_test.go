package lifecycle

import (
	"context"
	"encoding/json"
	"testing"

	apiv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/machinery/pkg/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/pganalyze/cnpg-i-pganalyze/internal/config"
	"github.com/pganalyze/cnpg-i-pganalyze/pkg/metadata"
)

func testPlugin(t *testing.T, apiKey string) *config.Plugin {
	t.Helper()
	plugin, err := config.NewPlugin("", apiKey, "", config.ResourceSettings{})
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	return plugin
}

func testCluster(parameters map[string]string) *apiv1.Cluster {
	return &apiv1.Cluster{
		TypeMeta:   metav1.TypeMeta{APIVersion: "postgresql.cnpg.io/v1", Kind: "Cluster"},
		ObjectMeta: metav1.ObjectMeta{Name: "main", Namespace: "prod"},
		Spec: apiv1.ClusterSpec{
			Plugins: []apiv1.PluginConfiguration{{Name: metadata.PluginName, Parameters: parameters}},
		},
	}
}

func testPod() *corev1.Pod {
	return &corev1.Pod{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{Name: "main-1", Namespace: "prod"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "postgres", Image: "postgres"}},
			Volumes: []corev1.Volume{{
				Name:         "scratch-data",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			}},
		},
	}
}

func mutate(t *testing.T, plugin *config.Plugin, cluster *apiv1.Cluster, pod *corev1.Pod) *corev1.Pod {
	t.Helper()
	settings, _ := config.ParseCluster(plugin, cluster.Spec.Plugins[0].Parameters)
	if err := NewImplementation(plugin).mutatePod(cluster, pod, settings, log.GetLogger()); err != nil {
		t.Fatalf("mutatePod: %v", err)
	}
	return pod
}

func envOf(container corev1.Container) map[string]corev1.EnvVar {
	result := map[string]corev1.EnvVar{}
	for _, env := range container.Env {
		result[env.Name] = env
	}
	return result
}

func sidecarOf(t *testing.T, pod *corev1.Pod) corev1.Container {
	t.Helper()
	for _, container := range pod.Spec.InitContainers {
		if container.Name == SidecarName {
			return container
		}
	}
	t.Fatal("collector sidecar not found among the init containers")
	return corev1.Container{}
}

func TestMutatePodInjectsSidecarAndLogExport(t *testing.T) {
	pod := mutate(t, testPlugin(t, "default-key"), testCluster(nil), testPod())

	sidecar := sidecarOf(t, pod)
	if sidecar.RestartPolicy == nil || *sidecar.RestartPolicy != corev1.ContainerRestartPolicyAlways {
		t.Errorf("the collector should be a native sidecar, got restartPolicy %v", sidecar.RestartPolicy)
	}

	env := envOf(sidecar)
	for name, want := range map[string]string{
		"PGA_API_KEY":          "default-key",
		"PGA_API_SYSTEM_ID":    "main-1",
		"PGA_API_SYSTEM_SCOPE": "prod/main",
		"API_CLUSTER_ID":       "prod/main",
		"DB_HOST":              "/controller/run",
		"DB_USERNAME":          "pganalyze",
		"DB_NAME":              "postgres,*",
		"LOG_OTEL_SERVER":      "127.0.0.1:4318",
	} {
		if env[name].Value != want {
			t.Errorf("%s = %q, want %q", name, env[name].Value, want)
		}
	}

	if got := envOf(pod.Spec.Containers[0])[otelLogsEndpointEnv].Value; got != "http://127.0.0.1:4318/v1/logs" {
		t.Errorf("postgres container log endpoint = %q", got)
	}
}

func TestMutatePodAutoSetup(t *testing.T) {
	withSetup := sidecarOf(t, mutate(t, testPlugin(t, "default-key"), testCluster(nil), testPod()))
	if len(withSetup.Args) != 1 || withSetup.Args[0] != "setup-and-collector" {
		t.Errorf("args = %v, want the setup variant by default", withSetup.Args)
	}

	cluster := testCluster(map[string]string{config.ParamAutoSetup: "false"})
	withoutSetup := sidecarOf(t, mutate(t, testPlugin(t, "default-key"), cluster, testPod()))
	if len(withoutSetup.Args) != 0 {
		t.Errorf("args = %v, want the image's default command", withoutSetup.Args)
	}
}

func TestMutatePodUsesClusterSecret(t *testing.T) {
	cluster := testCluster(map[string]string{config.ParamAPIKeySecretName: "pganalyze-key"})
	pod := mutate(t, testPlugin(t, "default-key"), cluster, testPod())

	apiKey := envOf(sidecarOf(t, pod))["PGA_API_KEY"]
	if apiKey.Value != "" || apiKey.ValueFrom == nil || apiKey.ValueFrom.SecretKeyRef.Name != "pganalyze-key" {
		t.Errorf("PGA_API_KEY should come from the cluster's Secret, got %+v", apiKey)
	}
}

func TestMutatePodLogsDisabled(t *testing.T) {
	cluster := testCluster(map[string]string{config.ParamLogs: "false"})
	pod := mutate(t, testPlugin(t, "default-key"), cluster, testPod())

	if _, ok := envOf(sidecarOf(t, pod))["LOG_OTEL_SERVER"]; ok {
		t.Error("the collector should not listen for logs")
	}
	if _, ok := envOf(pod.Spec.Containers[0])[otelLogsEndpointEnv]; ok {
		t.Error("the postgres container should not get a log endpoint")
	}
}

func TestMutatePodKeepsExistingLogExport(t *testing.T) {
	tests := map[string]func(*apiv1.Cluster, *corev1.Pod){
		"endpoint in the Cluster's env": func(cluster *apiv1.Cluster, pod *corev1.Pod) {
			own := corev1.EnvVar{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: "http://otel:4318"}
			cluster.Spec.Env = []corev1.EnvVar{own}
			pod.Spec.Containers[0].Env = []corev1.EnvVar{own}
		},
		"Cluster uses envFrom": func(cluster *apiv1.Cluster, _ *corev1.Pod) {
			cluster.Spec.EnvFrom = []corev1.EnvFromSource{{
				ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "otel"},
				},
			}}
		},
	}

	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			cluster, pod := testCluster(nil), testPod()
			prepare(cluster, pod)
			mutate(t, testPlugin(t, "default-key"), cluster, pod)

			if _, ok := envOf(pod.Spec.Containers[0])[otelLogsEndpointEnv]; ok {
				t.Error("the plugin must not add its own log endpoint")
			}
			if _, ok := envOf(sidecarOf(t, pod))["LOG_OTEL_SERVER"]; ok {
				t.Error("the collector should not listen for logs it won't receive")
			}
		})
	}
}

func TestMutatePodIsIdempotent(t *testing.T) {
	plugin, cluster := testPlugin(t, "default-key"), testCluster(nil)
	once := mutate(t, plugin, cluster, testPod())
	twice := mutate(t, plugin, cluster, once.DeepCopy())

	first, _ := json.Marshal(once)
	second, _ := json.Marshal(twice)
	if string(first) != string(second) {
		t.Errorf("a second pass changed the Pod:\n%s\n%s", first, second)
	}
}

func TestLifecycleHookLeavesPodAloneWithoutAPIKey(t *testing.T) {
	// No default API key and no Secret named: the Pod must still be created
	cluster, _ := json.Marshal(testCluster(nil))
	pod, _ := json.Marshal(testPod())

	response, err := NewImplementation(testPlugin(t, "")).LifecycleHook(context.Background(),
		&lifecycle.OperatorLifecycleRequest{
			OperationType:     &lifecycle.OperatorOperationType{Type: lifecycle.OperatorOperationType_TYPE_CREATE},
			ClusterDefinition: cluster,
			ObjectDefinition:  pod,
		})
	if err != nil {
		t.Fatalf("LifecycleHook: %v", err)
	}
	if len(response.GetJsonPatch()) != 0 {
		t.Errorf("expected no patch, got %s", response.GetJsonPatch())
	}
}

func TestMutatePodIgnoresInvalidParameters(t *testing.T) {
	cluster := testCluster(map[string]string{"logz": "true", config.ParamLogs: "maybe"})
	pod := mutate(t, testPlugin(t, "default-key"), cluster, testPod())

	if _, ok := envOf(sidecarOf(t, pod))["LOG_OTEL_SERVER"]; !ok {
		t.Error("an invalid logs value should keep the default of on")
	}
}

func TestLifecycleHookReturnsPatch(t *testing.T) {
	cluster, _ := json.Marshal(testCluster(nil))
	pod, _ := json.Marshal(testPod())

	response, err := NewImplementation(testPlugin(t, "default-key")).LifecycleHook(context.Background(),
		&lifecycle.OperatorLifecycleRequest{
			OperationType:     &lifecycle.OperatorOperationType{Type: lifecycle.OperatorOperationType_TYPE_EVALUATE},
			ClusterDefinition: cluster,
			ObjectDefinition:  pod,
		})
	if err != nil {
		t.Fatalf("LifecycleHook: %v", err)
	}
	if len(response.GetJsonPatch()) == 0 {
		t.Error("expected a patch that adds the collector")
	}
}
