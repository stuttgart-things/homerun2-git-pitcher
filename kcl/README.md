# KCL Deployment Manifests

KCL module for generating Kubernetes manifests for homerun2-git-pitcher.

## Prerequisites

- [KCL CLI](https://kcl-lang.io/) v0.11+

## Render manifests

```bash
# With default values
kcl run kcl/

# With test profile
kcl run kcl/ -Y tests/kcl-deploy-profile.yaml

# With namespace override
kcl run kcl/ -Y tests/kcl-deploy-profile.yaml -D 'config.namespace=my-namespace'
```

## Apply to cluster

```bash
kcl run kcl/ -Y tests/kcl-deploy-profile.yaml \
  -D 'config.namespace=homerun2' \
  -D 'config.redisAddr=redis-stack.homerun2.svc.cluster.local' \
  -D 'config.redisPassword=<password>' \
  -D 'config.githubToken=<token>' \
  | python3 -c "
import yaml, sys
data = yaml.safe_load(sys.stdin)
for m in data['manifests']:
    print('---')
    print(yaml.dump(m, default_flow_style=False).rstrip())
" | kubectl apply -f -
```

## Generated resources

| Resource | Name | Condition |
|----------|------|-----------|
| Namespace | `{name}` | always |
| ServiceAccount | `{name}` | always |
| ConfigMap | `{name}-config` | always |
| ConfigMap | `{name}-watch-config` | `watchConfigYaml` set |
| Secret | `{name}-redis` | `redisPassword` set |
| Secret | `{name}-github` | `githubToken` set |
| Deployment | `{name}` | always |
| Service | `{name}` | always |
| HTTPRoute | `{name}` | `httpRouteEnabled: true` |

## Configuration

All values are set via `-D 'config.<key>=<value>'` or a YAML profile file (`-Y`).

| Key | Default | Description |
|-----|---------|-------------|
| `image` | `ghcr.io/stuttgart-things/homerun2-git-pitcher:latest` | Container image |
| `namespace` | `homerun2` | Target namespace |
| `replicas` | `1` | Replica count |
| `redisAddr` | `redis-stack.homerun2.svc.cluster.local` | Redis host |
| `redisPort` | `6379` | Redis port |
| `redisStream` | `homerun` | Redis stream name |
| `redisPassword` | (empty) | Redis password (creates Secret) |
| `authToken` | (empty) | Bearer token for `/pitch` endpoint |
| `githubToken` | (empty) | GitHub PAT (creates Secret) |
| `watchConfigYaml` | (empty) | Watch profile YAML (creates ConfigMap) |
| `dedupStatePath` | `/data/dedup-state.json` | Dedup state file path |
| `httpRouteEnabled` | `false` | Enable Gateway API HTTPRoute |
| `httpRouteHostname` | (empty) | HTTPRoute hostname |
| `httpRouteParentRefName` | (empty) | Gateway name |
| `httpRouteParentRefNamespace` | (empty) | Gateway namespace |
| `trustBundleConfigMap` | (empty) | ConfigMap with `trust-bundle.pem` |
| `extraEnvVars` | `{}` | Additional env vars for the ConfigMap |

## OCI artifact

The release workflow publishes this KCL module as an OCI artifact to `ghcr.io/stuttgart-things/homerun2-git-pitcher-kcl`. Flux components reference this artifact via `OCIRepository`.
