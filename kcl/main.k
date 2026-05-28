import labels
import serviceaccount as sa
import configmap as cm
import secret as sec
import deploy as dep
import service as svc
import httproute as hr

# Namespace is NOT emitted from the KCL profile — the consuming Application
# uses `CreateNamespace=true`, and PR-preview namespaces are pre-created by
# Kyverno. See stuttgart-things/homerun2-omni-pitcher#132.
manifests = [
    m for m in [
        sa.serviceAccount
        cm.configMap
        cm.watchConfigMap
        sec.secretRedis
        sec.secretGithub
        dep.deployment
        svc._service
        hr._httproute
    ] if m
]
