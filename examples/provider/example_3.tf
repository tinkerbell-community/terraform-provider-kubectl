provider "kubectl" {
  apply_retry_count      = 15
  host                   = var.eks_cluster_endpoint
  cluster_ca_certificate = base64decode(var.eks_cluster_ca)
  load_config_file       = false

  exec {
    api_version = "client.authentication.k8s.io/v1"
    command     = "aws-iam-authenticator"
    args = [
      "token",
      "-i",
      module.eks.cluster_id,
    ]
  }
}
