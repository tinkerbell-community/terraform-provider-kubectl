data "kubectl_server_version" "current" {}

output "kubernetes_info" {
  value = {
    version     = data.kubectl_server_version.current.version
    major       = data.kubectl_server_version.current.major
    minor       = data.kubectl_server_version.current.minor
    git_version = data.kubectl_server_version.current.git_version
    platform    = data.kubectl_server_version.current.platform
  }
}

# Use in conditional logic
locals {
  k8s_version_major = tonumber(data.kubectl_server_version.current.major)
  k8s_version_minor = tonumber(data.kubectl_server_version.current.minor)

  supports_feature = local.k8s_version_major >= 1 && local.k8s_version_minor >= 24
}
