provider "kubectl" {
  # Configuration can be provided via environment variables or explicitly

  # Load kubeconfig from default location
  # load_config_file = true

  # Or specify a custom kubeconfig path
  # config_path = "~/.kube/config"

  # Or specify explicit cluster connection details
  # host = "https://cluster.example.com"
  # token = "your-token-here"
  # cluster_ca_certificate = file("ca.crt")

  # Number of retries for apply operations (default: 1)
  # apply_retry_count = 3
}
