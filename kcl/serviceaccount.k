"""
ServiceAccount for homerun2-git-pitcher
"""

import labels

_config = labels.config

serviceAccount = {
    apiVersion: "v1"
    kind: "ServiceAccount"
    metadata: {
        name: _config.name
        namespace: _config.namespace
        labels: labels.commonLabels
    }
}
