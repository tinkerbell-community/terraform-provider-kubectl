terraform {
  required_version = ">= 0.13"

  required_providers {
    kubectl = {
      source  = "hashicorp-oss/kubectl"
      version = ">= 2.0.0"
    }
  }
}
