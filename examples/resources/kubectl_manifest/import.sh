# kubectl_manifest can be imported using the format: apiVersion//kind//name//namespace
# For cluster-scoped resources, omit the namespace

# Import a namespaced ConfigMap
terraform import kubectl_manifest.example "v1//ConfigMap//example-config//default"

# Import a cluster-scoped resource (ClusterRole)
terraform import kubectl_manifest.cluster_role "rbac.authorization.k8s.io/v1//ClusterRole//cluster-admin"

# Import a Deployment
terraform import kubectl_manifest.deployment "apps/v1//Deployment//nginx-deployment//default"
