"""
Service for homerun2-git-pitcher
"""

import labels

_config = labels.config

_service = {
    apiVersion: "v1"
    kind: "Service"
    metadata: {
        name: _config.name
        namespace: _config.namespace
        labels: labels.commonLabels
    }
    spec: {
        type: _config.serviceType
        ports: [
            {
                name: "http"
                port: _config.servicePort
                targetPort: "http"
                protocol: "TCP"
            }
        ]
        selector: labels.selectorLabels
    }
}
