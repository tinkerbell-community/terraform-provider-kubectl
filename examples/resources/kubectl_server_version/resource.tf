resource "kubectl_server_version" "example" {
  triggers = {
    # Force refresh when this value changes
    always_refresh = timestamp()
  }
}

output "kubernetes_version" {
  value = {
    version     = kubectl_server_version.example.version
    major       = kubectl_server_version.example.major
    minor       = kubectl_server_version.example.minor
    patch       = kubectl_server_version.example.patch
    git_version = kubectl_server_version.example.git_version
    git_commit  = kubectl_server_version.example.git_commit
    build_date  = kubectl_server_version.example.build_date
    platform    = kubectl_server_version.example.platform
  }
}
