"""
Secrets for homerun2-git-pitcher (Redis password, GitHub token)
"""

import base64
import labels

_config = labels.config

# Secret - Redis Password
secretRedis = {
    apiVersion: "v1"
    kind: "Secret"
    metadata: {
        name: "{}-redis".format(_config.name)
        namespace: _config.namespace
        labels: labels.commonLabels
    }
    type: "Opaque"
    data: {
        password: base64.encode(_config.redisPassword)
    }
} if _config.redisPassword else None

# Secret - GitHub Token
secretGithub = {
    apiVersion: "v1"
    kind: "Secret"
    metadata: {
        name: "{}-github".format(_config.name)
        namespace: _config.namespace
        labels: labels.commonLabels
    }
    type: "Opaque"
    data: {
        token: base64.encode(_config.githubToken)
    }
} if _config.githubToken else None
