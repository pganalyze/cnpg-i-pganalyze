# cnpg-i-pganalyze

A [CNPG-I](https://github.com/cloudnative-pg/cnpg-i) plugin that monitors
[CloudNativePG](https://cloudnative-pg.io/) clusters with
[pganalyze](https://pganalyze.com/). It adds the
[pganalyze collector](https://github.com/pganalyze/collector) as a sidecar to
every instance Pod of the clusters that enable it.

Status: early development.

## How it works

- The collector connects to Postgres over the Pod's Unix socket as the
  `pganalyze` role, using peer authentication. No password or Secret is needed.
- Every instance reports as its own pganalyze server, identified by its Pod
  name with `namespace/cluster` as the scope. The same `namespace/cluster` is
  set as the cluster ID, which pganalyze uses to group the instances.
- Postgres logs are pushed to the collector by the CloudNativePG instance
  manager over OTLP, inside the Pod. This needs a CloudNativePG version with
  [OpenTelemetry log export](https://github.com/cloudnative-pg/cloudnative-pg/pull/11783);
  on older versions the collector runs without logs.
- Before starting, the sidecar creates the `pganalyze` role and the collector's
  helper functions on the primary, connecting as `postgres` over the socket
  (turn this off with `autoSetup: "false"`). This needs a collector image with
  the `setup-and-collector` command.
- The plugin never changes Postgres settings. Missing prerequisites are
  reported in the Cluster's `status.pluginStatus`.

## Install

Requires CloudNativePG and cert-manager.

```bash
# Default API key, used by clusters that don't name their own Secret
kubectl -n cnpg-system create secret generic cnpg-i-pganalyze \
  --from-literal=PGA_API_KEY=your-api-key

kubectl apply -k kubernetes/
```

The default API key is passed to the collector sidecars as a plain
environment value, so it is visible to anyone who can read Pods in a
monitored cluster's namespace.

## Enable it for a Cluster

```yaml
spec:
  plugins:
  - name: cnpg-i-pganalyze.pganalyze.com
  postgresql:
    pg_ident:
    - local postgres pganalyze
    parameters:
      pg_stat_statements.track: "all"
```

See [doc/examples/cluster.yaml](doc/examples/cluster.yaml). Enabling the
plugin restarts the instances, as the sidecar is added to their Pods.

### Parameters

All are optional when the plugin has a default API key.

| Parameter | Default | Description |
|---|---|---|
| `apiKeySecretName` | plugin default key | Secret in the Cluster's namespace holding the API key |
| `apiKeySecretKey` | `PGA_API_KEY` | Key within that Secret |
| `apiBaseUrl` | pganalyze cloud | API endpoint, for pganalyze Enterprise Server |
| `databases` | `postgres,*` | Databases to monitor (the collector's `db_name`) |
| `logs` | `true` | Send Postgres logs to the collector |
| `autoSetup` | `true` | Create the `pganalyze` role and helper functions before starting the collector. When off, create them yourself. |
| `image` | plugin default | Collector image |

An invalid parameter keeps its default and is reported in the Cluster's
status. CloudNativePG does not call plugins to validate a Cluster when it is
created or changed, so it can't be rejected up front.

Logs are left alone if the Cluster already sets an `OTEL_EXPORTER_OTLP_*`
endpoint in `env`, or uses `envFrom`: the instance manager exports to a
single destination, and one configured by the user wins.

### Status

```bash
kubectl get cluster cluster-example -o jsonpath='{.status.pluginStatus[0].status}'
```

## Development

```bash
make test
make image CONTAINER_TOOL=podman
```

## License

cnpg-i-pganalyze is licensed under the Apache License 2.0, see LICENSE file for details.

Copyright (c) 2026, Duboce Labs, Inc. (pganalyze)
