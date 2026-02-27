terraform {
  required_version = ">= 0.13"

  required_providers {
    kubectl = {
      source  = "tinkerbell-community/kubectl"
      version = ">= 2.0.0"
    }
  }
}
