# Deployment

## Container image

Built with [ko](https://ko.build/) using a distroless base image (`cgr.dev/chainguard/static:latest`).

```bash
# Pull from ghcr.io
docker pull ghcr.io/stuttgart-things/homerun2-git-pitcher:<tag>
```

### Build locally

```bash
# With ko directly
ko build .

# Via Taskfile + Dagger
task build-scan-image-ko
```

## Docker run

### HTTP API only (no watcher)

```bash
docker run -d \
  -e REDIS_ADDR=redis \
  -e REDIS_PORT=6379 \
  -e REDIS_STREAM=messages \
  -e AUTH_TOKEN=mysecret \
  -p 8080:8080 \
  ghcr.io/stuttgart-things/homerun2-git-pitcher:<tag>
```

### With GitHub watcher

Mount the watch profile and set the GitHub token:

```bash
docker run -d \
  -e REDIS_ADDR=redis \
  -e REDIS_PORT=6379 \
  -e REDIS_STREAM=messages \
  -e AUTH_TOKEN=mysecret \
  -e GITHUB_TOKEN=ghp_... \
  -e WATCH_CONFIG=/config/watch-profile.yaml \
  -e DEDUP_STATE_FILE=/data/dedup-state.json \
  -v ./tests/watch-profile.yaml:/config/watch-profile.yaml:ro \
  -v pitcher-data:/data \
  -p 8080:8080 \
  ghcr.io/stuttgart-things/homerun2-git-pitcher:<tag>
```

## KCL Deployment (recommended)

The recommended Kubernetes deployment method uses [KCL](https://kcl-lang.io/) manifests in the `kcl/` directory. This generates all resources (Namespace, ServiceAccount, ConfigMaps, Secrets, Deployment, Service, HTTPRoute) from a single configuration schema.

### Render manifests

```bash
# Preview with default values
kcl run kcl/

# Preview with test profile
kcl run kcl/ -Y tests/kcl-deploy-profile.yaml

# Override specific values
kcl run kcl/ -Y tests/kcl-deploy-profile.yaml \
  -D 'config.namespace=homerun2-flux' \
  -D 'config.image=ghcr.io/stuttgart-things/homerun2-git-pitcher:v0.4.0'
```

### Apply to cluster

The KCL output wraps manifests in a `manifests:` list. Convert to multi-document YAML before applying:

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

### Configuration reference

See [kcl/README.md](../kcl/README.md) for the full list of configuration keys and generated resources.

### OCI artifact

The CI/CD pipeline publishes the KCL module as an OCI artifact to `ghcr.io/stuttgart-things/homerun2-git-pitcher-kcl`. The Flux component references this artifact.

## Kubernetes (manual)

### Required secrets

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: git-pitcher-secrets
type: Opaque
stringData:
  AUTH_TOKEN: "your-auth-token"
  GITHUB_TOKEN: "ghp_your-github-pat"
```

### Watch profile ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: git-pitcher-watch-config
data:
  watch-profile.yaml: |
    github:
      token: env:GITHUB_TOKEN
      repos:
        - owner: stuttgart-things
          name: homerun2-led-catcher
          interval: 5m
          events: [push, pull_request, release, workflow_run]
```

### Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: homerun2-git-pitcher
spec:
  replicas: 1
  selector:
    matchLabels:
      app: homerun2-git-pitcher
  template:
    metadata:
      labels:
        app: homerun2-git-pitcher
    spec:
      containers:
        - name: git-pitcher
          image: ghcr.io/stuttgart-things/homerun2-git-pitcher:<tag>
          ports:
            - containerPort: 8080
          env:
            - name: REDIS_ADDR
              value: redis
            - name: REDIS_PORT
              value: "6379"
            - name: REDIS_STREAM
              value: messages
            - name: WATCH_CONFIG
              value: /config/watch-profile.yaml
            - name: DEDUP_STATE_FILE
              value: /data/dedup-state.json
            - name: LOG_LEVEL
              value: info
          envFrom:
            - secretRef:
                name: git-pitcher-secrets
          volumeMounts:
            - name: watch-config
              mountPath: /config
              readOnly: true
            - name: dedup-state
              mountPath: /data
          livenessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 30
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 3
            periodSeconds: 10
      volumes:
        - name: watch-config
          configMap:
            name: git-pitcher-watch-config
        - name: dedup-state
          emptyDir: {}
```

## Flux app deployment

The recommended way to deploy the full homerun2 stack is via the [homerun2 Flux app](https://github.com/stuttgart-things/flux/tree/main/apps/homerun2). It uses Kustomize Components to deploy Redis Stack + all homerun2 microservices into a shared namespace.

See the [Flux app README](https://github.com/stuttgart-things/flux/tree/main/apps/homerun2) for substitution variables and cluster examples.

## Health check

```bash
curl -s http://localhost:8080/health | jq .
```

Response includes rate limit status when the watcher is active:

```json
{
  "status": "healthy",
  "version": "1.2.0",
  "commit": "abc1234",
  "date": "2026-03-18",
  "rateLimit": {
    "limit": 5000,
    "remaining": 4850,
    "reset": "2026-03-18T09:00:00Z",
    "backingOff": false
  }
}
```
