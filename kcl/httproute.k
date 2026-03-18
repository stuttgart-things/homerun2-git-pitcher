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
        parentRefs: [
            {
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
                backendRefs: [
                    {
                        name: _config.name
                        port: _config.servicePort
                    }
                ]
            }
        ]
    }
} if _config.httpRouteEnabled else None
