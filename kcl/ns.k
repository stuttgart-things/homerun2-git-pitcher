"""
Namespace for homerun2-git-pitcher
"""

import labels

_config = labels.config

_namespace = {
    apiVersion: "v1"
    kind: "Namespace"
    metadata: {
        name: _config.namespace
        labels: labels.commonLabels
    }
}
