"""
Deployment for homerun2-git-pitcher
"""

import labels

_config = labels.config

deployment = {
    apiVersion: "apps/v1"
    kind: "Deployment"
    metadata: {
        name: _config.name
        namespace: _config.namespace
        labels: labels.commonLabels
    }
    spec: {
        replicas: _config.replicas
        selector: {
            matchLabels: labels.selectorLabels
        }
        strategy: {
            type: "RollingUpdate"
            rollingUpdate: {
                maxSurge: 1
                maxUnavailable: 0
            }
        }
        template: {
            metadata: {
                labels: labels.commonLabels
            }
            spec: {
                serviceAccountName: _config.name
                containers: [
                    {
                        name: _config.name
                        image: _config.image
                        imagePullPolicy: _config.imagePullPolicy
                        ports: [
                            {
                                name: "http"
                                containerPort: _config.containerPort
                                protocol: "TCP"
                            }
                        ]
                        env: [
                            {
                                name: "PORT"
                                value: str(_config.containerPort)
                            }
                            {
                                name: "PITCHER_MODE"
                                value: "redis"
                            }
                            {
                                name: "REDIS_ADDR"
                                value: _config.redisAddr
                            }
                            {
                                name: "REDIS_PORT"
                                value: str(_config.redisPort)
                            }
                            {
                                name: "REDIS_STREAM"
                                value: _config.redisStream
                            }
                            if _config.redisPassword:
                                {
                                    name: "REDIS_PASSWORD"
                                    valueFrom: {
                                        secretKeyRef: {
                                            name: "{}-redis".format(_config.name)
                                            key: "password"
                                        }
                                    }
                                }
                            if _config.authToken:
                                {
                                    name: "AUTH_TOKEN"
                                    value: _config.authToken
                                }
                        ] + ([
                            {
                                name: "WATCH_CONFIG"
                                value: "/config/watch-profile.yaml"
                            }
                            {
                                name: "DEDUP_STATE_FILE"
                                value: _config.dedupStatePath
                            }
                            {
                                name: "GITHUB_TOKEN"
                                valueFrom: {
                                    secretKeyRef: {
                                        name: "{}-github".format(_config.name)
                                        key: "token"
                                    }
                                }
                            }
                        ] if _config.watchConfigYaml and _config.githubToken else []) + ([
                            {
                                name: "SSL_CERT_DIR"
                                value: "/etc/ssl/custom"
                            }
                        ] if _config.trustBundleConfigMap else [])
                        envFrom: [
                            {
                                configMapRef: {
                                    name: "{}-config".format(_config.name)
                                }
                            }
                        ]
                        resources: {
                            requests: {
                                cpu: _config.cpuRequest
                                memory: _config.memoryRequest
                            }
                            limits: {
                                cpu: _config.cpuLimit
                                memory: _config.memoryLimit
                            }
                        }
                        livenessProbe: {
                            httpGet: {
                                path: "/health"
                                port: "http"
                            }
                            initialDelaySeconds: 10
                            periodSeconds: 10
                            timeoutSeconds: 5
                            failureThreshold: 3
                        }
                        readinessProbe: {
                            httpGet: {
                                path: "/health"
                                port: "http"
                            }
                            initialDelaySeconds: 5
                            periodSeconds: 5
                            timeoutSeconds: 3
                            failureThreshold: 3
                        }
                        securityContext: {
                            readOnlyRootFilesystem: True
                            runAsNonRoot: True
                            allowPrivilegeEscalation: False
                            capabilities: {
                                drop: ["ALL"]
                            }
                        }
                        volumeMounts: [
                            {
                                name: "tmp"
                                mountPath: "/tmp"
                            }
                            {
                                name: "dedup-state"
                                mountPath: "/data"
                            }
                        ] + ([
                            {
                                name: "watch-config"
                                mountPath: "/config"
                                readOnly: True
                            }
                        ] if _config.watchConfigYaml else []) + ([
                            {
                                name: "trust-bundle"
                                mountPath: "/etc/ssl/custom/trust-bundle.pem"
                                subPath: "trust-bundle.pem"
                                readOnly: True
                            }
                        ] if _config.trustBundleConfigMap else [])
                    }
                ]
                securityContext: {
                    runAsNonRoot: True
                    runAsUser: 65532
                    runAsGroup: 65532
                    fsGroup: 65532
                    seccompProfile: {
                        type: "RuntimeDefault"
                    }
                }
                volumes: [
                    {
                        name: "tmp"
                        emptyDir: {}
                    }
                    {
                        name: "dedup-state"
                        emptyDir: {}
                    }
                ] + ([
                    {
                        name: "watch-config"
                        configMap: {
                            name: "{}-watch-config".format(_config.name)
                        }
                    }
                ] if _config.watchConfigYaml else []) + ([
                    {
                        name: "trust-bundle"
                        configMap: {
                            name: _config.trustBundleConfigMap
                            optional: True
                        }
                    }
                ] if _config.trustBundleConfigMap else [])
                affinity: {
                    podAntiAffinity: {
                        preferredDuringSchedulingIgnoredDuringExecution: [
                            {
                                weight: 100
                                podAffinityTerm: {
                                    labelSelector: {
                                        matchLabels: labels.selectorLabels
                                    }
                                    topologyKey: "kubernetes.io/hostname"
                                }
                            }
                        ]
                    }
                }
            }
        }
    }
}
