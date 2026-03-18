import labels
import ns
import serviceaccount as sa
import configmap as cm
import secret as sec
import deploy as dep
import service as svc
import httproute as hr

manifests = [
    m for m in [
        ns._namespace
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
