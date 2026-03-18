"""
Common labels for homerun2-git-pitcher resources
"""

import schema

# Get command-line options
_image = option("config.image")
_namespace = option("config.namespace")
_redisAddr = option("config.redisAddr")
_redisPort = option("config.redisPort")
_redisStream = option("config.redisStream")
_redisPassword = option("config.redisPassword")
_authToken = option("config.authToken")
_watchConfigYaml = option("config.watchConfigYaml")
_githubToken = option("config.githubToken")
_dedupStatePath = option("config.dedupStatePath")
_httpRouteEnabled = option("config.httpRouteEnabled")
_httpRouteParentRefName = option("config.httpRouteParentRefName")
_httpRouteParentRefNamespace = option("config.httpRouteParentRefNamespace")
_httpRouteHostname = option("config.httpRouteHostname")
_httpRouteAnnotations = option("config.httpRouteAnnotations") or {}
_trustBundleConfigMap = option("config.trustBundleConfigMap")
_extraEnvVars = option("config.extraEnvVars") or {}

# Config instance with command-line overrides
config: schema.GitPitcher = schema.GitPitcher {
    if _image:
        image = _image
    if _namespace:
        namespace = _namespace
    if _redisAddr:
        redisAddr = _redisAddr
    if _redisPort:
        redisPort = _redisPort
    if _redisStream:
        redisStream = _redisStream
    if _redisPassword:
        redisPassword = _redisPassword
    if _authToken:
        authToken = _authToken
    if _watchConfigYaml:
        watchConfigYaml = _watchConfigYaml
    if _githubToken:
        githubToken = _githubToken
    if _dedupStatePath:
        dedupStatePath = _dedupStatePath
    if _httpRouteEnabled:
        httpRouteEnabled = _httpRouteEnabled in [True, "True", "true"]
    if _httpRouteParentRefName:
        httpRouteParentRefName = _httpRouteParentRefName
    if _httpRouteParentRefNamespace:
        httpRouteParentRefNamespace = _httpRouteParentRefNamespace
    if _httpRouteHostname:
        httpRouteHostname = _httpRouteHostname
    if _httpRouteAnnotations:
        httpRouteAnnotations = _httpRouteAnnotations
    if _trustBundleConfigMap:
        trustBundleConfigMap = _trustBundleConfigMap
    if _extraEnvVars:
        extraEnvVars = _extraEnvVars
}

# Common labels applied to all resources
commonLabels = {
    "app.kubernetes.io/name": config.name
    "app.kubernetes.io/instance": config.name
    "app.kubernetes.io/component": "pitcher"
    "app.kubernetes.io/part-of": "homerun2"
    **config.labels
}

# Selector labels for pods
selectorLabels = {
    "app.kubernetes.io/name": config.name
    "app.kubernetes.io/instance": config.name
}
