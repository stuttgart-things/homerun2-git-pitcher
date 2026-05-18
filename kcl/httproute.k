"""
HTTPRoute (Gateway API) for homerun2-git-pitcher
"""

import labels

_config = labels.config

_hostname = _config.httpRouteHostname

_httproute = {
    apiVersion: "gateway.networking.k8s.io/v1"
    kind: "HTTPRoute"
    metadata: {
        name: _config.name
        namespace: _config.namespace
        labels: labels.commonLabels
        if _config.annotations or _config.httpRouteAnnotations:
            annotations: {
                **_config.annotations
                **_config.httpRouteAnnotations
            }
    }
    spec: {
        # group + kind are admission-webhook defaults on parentRefs; rendering
        # them explicitly avoids drift on every reconcile (Gateway controller
        # adds them in, ArgoCD compares chart-rendered against live and flags
        # OutOfSync otherwise). Same shape as stuttgart-things/argocd#114
        # fix for the standalone httproute chart.
        parentRefs: [
            {
                group: "gateway.networking.k8s.io"
                kind: "Gateway"
                name: _config.httpRouteParentRefName
                if _config.httpRouteParentRefNamespace:
                    namespace: _config.httpRouteParentRefNamespace
            }
        ]
        if _hostname:
            hostnames: [_hostname]
        rules: [
            {
                matches: [
                    {
                        path: {
                            type: "PathPrefix"
                            value: "/"
                        }
                    }
                ]
                # group + kind + weight are admission defaults on backendRefs;
                # rendering them explicitly prevents the same drift.
                backendRefs: [
                    {
                        group: ""
                        kind: "Service"
                        name: _config.name
                        port: _config.servicePort
                        weight: 1
                    }
                ]
            }
        ]
    }
} if _config.httpRouteEnabled else None
