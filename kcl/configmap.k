"""
ConfigMap for homerun2-git-pitcher
"""

import labels

_config = labels.config

configMap = {
    apiVersion: "v1"
    kind: "ConfigMap"
    metadata: {
        name: "{}-config".format(_config.name)
        namespace: _config.namespace
        labels: labels.commonLabels
    }
    data: {
        LOG_LEVEL: "info"
        **_config.extraEnvVars
    }
}

# Watch config ConfigMap (mounted as file)
watchConfigMap = {
    apiVersion: "v1"
    kind: "ConfigMap"
    metadata: {
        name: "{}-watch-config".format(_config.name)
        namespace: _config.namespace
        labels: labels.commonLabels
    }
    data: {
        "watch-profile.yaml": _config.watchConfigYaml
    }
} if _config.watchConfigYaml else None
