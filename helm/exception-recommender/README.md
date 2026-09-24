# exception-recommender

A Helm chart for exception-recommender, which bridges legacy Kyverno PolicyExceptions to Giant Swarm PolicyExceptions during the CEL migration.

**Homepage:** <https://github.com/giantswarm/exception-recommender>

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| name | string | `"exception-recommender"` |  |
| serviceType | string | `"managed"` |  |
| global.image.registry | string | `"gsoci.azurecr.io"` |  |
| global.podSecurityStandards.enforced | bool | `true` |  |
| cleanupJob.enabled | bool | `true` |  |
| ciliumNetworkPolicy.enabled | bool | `true` |  |
| image.registry | string | `"gsoci.azurecr.io"` |  |
| image.name | string | `"giantswarm/exception-recommender"` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| crds.install | bool | `true` |  |
| crds.image.tag | string | `"1.32.0"` |  |
| crds.resources.requests.cpu | string | `"100m"` |  |
| crds.resources.requests.memory | string | `"256Mi"` |  |
| crds.resources.limits.cpu | string | `"200m"` |  |
| crds.resources.limits.memory | string | `"512Mi"` |  |
| nodeSelector | object | `{}` |  |
| tolerations | list | `[]` |  |
| podLabels | object | `{}` |  |
| podSecurityContext.runAsUser | int | `1000` |  |
| podSecurityContext.runAsGroup | int | `1000` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| securityContext.allowPrivilegeEscalation | bool | `false` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| securityContext.privileged | bool | `false` |  |
| securityContext.readOnlyRootFilesystem | bool | `true` |  |
| securityContext.runAsNonRoot | bool | `true` |  |
| securityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| resources.requests.cpu | string | `"100m"` |  |
| resources.requests.memory | string | `"220Mi"` |  |
| resources.limits.cpu | string | `"100m"` |  |
| resources.limits.memory | string | `"220Mi"` |  |
| recommender.destinationNamespace | string | `"policy-exceptions"` |  |
| recommender.targetWorkloads[0] | string | `"Deployment"` |  |
| recommender.targetWorkloads[1] | string | `"DaemonSet"` |  |
| recommender.targetWorkloads[2] | string | `"StatefulSet"` |  |
| recommender.targetWorkloads[3] | string | `"CronJob"` |  |
| recommender.targetCategories[0] | string | `"Pod Security Standards (Baseline)"` |  |
| recommender.targetCategories[1] | string | `"Pod Security Standards (Restricted)"` |  |
| recommender.targetCategories[2] | string | `"Pod Security Standards"` |  |
| recommender.excludeNamespaces[0] | string | `"kube-system"` |  |
| recommender.excludeNamespaces[1] | string | `"giantswarm"` |  |
| recommender.enableAutomatedExceptions | bool | `false` |  |
| recommender.createNamespace | bool | `false` |  |
| migrationBridges.enabled | bool | `true` |  |
| migrationBridges.namespace | string | `"policy-exceptions"` |  |
