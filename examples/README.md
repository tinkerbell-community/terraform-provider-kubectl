# Terraform Provider Kubectl - Examples

This directory contains comprehensive examples for using the kubectl Terraform provider.

## Directory Structure

```
examples/
├── provider/              # Provider configuration examples
├── data-sources/          # Data source examples
│   ├── kubectl_file_documents/
│   ├── kubectl_filename_list/
│   ├── kubectl_path_documents/
│   └── kubectl_server_version/
└── resources/            # Resource examples
    ├── kubectl_manifest/
    └── kubectl_server_version/
```

## Provider Configuration

See [provider/provider.tf](provider/provider.tf) for examples of:
- Loading kubeconfig from default location
- Specifying custom kubeconfig path
- Using explicit cluster connection details
- Configuring retry behavior

## Resources

### kubectl_manifest

The `kubectl_manifest` resource allows you to deploy and manage any Kubernetes resource using YAML manifests.

**Basic Examples** - [resources/kubectl_manifest/resource.tf](resources/kubectl_manifest/resource.tf):
- ConfigMap
- Service
- Deployment with server-side apply and rollout wait
- Basic and complex Ingress
- RBAC (ServiceAccount and ClusterRoleBinding)
- Custom Resource Definitions (CRD) and Custom Resources
- Namespace override
- Ignoring fields for drift detection

**Advanced Examples** - [resources/kubectl_manifest/advanced.tf](resources/kubectl_manifest/advanced.tf):
- StatefulSet with custom wait conditions
- Server-side apply with field manager
- Sensitive fields obfuscation
- Force recreation on changes
- Ignoring managed fields
- Job with completion wait
- DaemonSet with rollout wait
- Multi-environment deployments
- Schema validation
- Complete application stack

**Import Examples** - [resources/kubectl_manifest/import.sh](resources/kubectl_manifest/import.sh):
- Importing namespaced resources
- Importing cluster-scoped resources
- Import format and syntax

**Key Features:**
- **Server-side apply**: Use `server_side_apply = true` for better conflict resolution
- **Wait for rollout**: Set `wait_for_rollout = true` to wait for Deployment/StatefulSet/DaemonSet to be ready
- **Custom wait conditions**: Use `wait_for` blocks to wait for specific field values
- **Sensitive fields**: Mark fields as sensitive to obfuscate them in logs
- **Namespace override**: Override the namespace specified in YAML
- **Drift detection**: Use `ignore_fields` to ignore specific fields during drift detection
- **Schema validation**: Enable/disable Kubernetes OpenAPI schema validation

### kubectl_server_version

See [resources/kubectl_server_version/resource.tf](resources/kubectl_server_version/resource.tf) for:
- Retrieving Kubernetes cluster version information
- Using triggers to force refresh
- Accessing version components (major, minor, patch)

## Data Sources

### kubectl_file_documents

See [data-sources/kubectl_file_documents/data-source.tf](data-sources/kubectl_file_documents/data-source.tf) for:
- Splitting multi-document YAML strings
- Creating resources from each document
- Working with CRDs and Custom Resources
- Accessing documents by manifest key
- Loading from file() function
- Managing multiple namespaces

**Key Features:**
- Splits YAML content on `---` document separators
- Returns both list of documents and map of manifests by key
- Keys format: `Kind.Namespace.Name` or `Kind.Name` for cluster-scoped

### kubectl_filename_list

See [data-sources/kubectl_filename_list/data-source.tf](data-sources/kubectl_filename_list/data-source.tf) for:
- Listing files matching glob patterns
- Getting basenames and full paths

**Key Features:**
- Supports standard glob patterns (`*`, `**`, `?`)
- Returns both basenames and full file paths

### kubectl_path_documents

See [data-sources/kubectl_path_documents/data-source.tf](data-sources/kubectl_path_documents/data-source.tf) for:
- Loading YAML files from directories
- Variable substitution in YAML files
- HCL templating with conditionals and loops
- Sensitive variables
- Disabling templating for raw YAML

**Example YAML Files**: See [data-sources/kubectl_path_documents/manifests/](data-sources/kubectl_path_documents/manifests/)

**Key Features:**
- **Variable substitution**: Use `${variable_name}` in YAML files
- **HCL templating**: Supports `%{ if }`, `%{ for }`, and other HCL directives
- **Sensitive variables**: Keep secrets out of state using `sensitive_vars`
- **Template functions**: Access Terraform functions like `split()`, `file()`, etc.
- **Disable templating**: Set `disable_template = true` for raw YAML

**Template Examples:**
- Simple variable substitution: `kind: ${the_kind}`
- Conditionals: `%{ if condition }...%{ else }...%{ endif }`
- Loops: `%{ for item in list }...%{ endfor }`
- Multiple documents with templating

### kubectl_server_version

See [data-sources/kubectl_server_version/data-source.tf](data-sources/kubectl_server_version/data-source.tf) for:
- Retrieving current Kubernetes cluster version
- Using version information in conditional logic
- Feature detection based on Kubernetes version

**Key Features:**
- Returns version components: major, minor, patch
- Includes git version, commit, and build date
- Platform information

## Common Patterns

### Multi-document YAML Processing

Use `kubectl_file_documents` or `kubectl_path_documents` with `for_each`:

```hcl
data "kubectl_file_documents" "manifests" {
  content = file("${path.module}/manifests.yaml")
}

resource "kubectl_manifest" "resources" {
  for_each  = toset(data.kubectl_file_documents.manifests.documents)
  yaml_body = each.value
}
```

### Template-driven Deployments

Use `kubectl_path_documents` with variables:

```hcl
data "kubectl_path_documents" "app" {
  pattern = "./manifests/*.yaml"
  
  vars = {
    namespace = var.environment
    replicas  = var.replica_count
    image     = var.app_image
  }
  
  sensitive_vars = {
    api_key = var.api_key
  }
}

resource "kubectl_manifest" "app" {
  for_each  = toset(data.kubectl_path_documents.app.documents)
  yaml_body = each.value
}
```

### Ordered Resource Creation

Use `depends_on` for resources that must be created in order:

```hcl
resource "kubectl_manifest" "namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: myapp
  YAML
}

resource "kubectl_manifest" "config" {
  depends_on = [kubectl_manifest.namespace]
  yaml_body  = "..."
}

resource "kubectl_manifest" "deployment" {
  depends_on = [kubectl_manifest.config]
  yaml_body  = "..."
  
  wait_for_rollout = true
}
```

### Multi-environment Deployments

Use `for_each` with `override_namespace`:

```hcl
locals {
  environments = toset(["dev", "staging", "prod"])
}

resource "kubectl_manifest" "app" {
  for_each = local.environments

  yaml_body = templatefile("${path.module}/app.yaml", {
    environment = each.key
  })

  override_namespace = each.key
  wait_for_rollout   = true
}
```

## Running the Examples

1. **Initialize Terraform:**
   ```bash
   terraform init
   ```

2. **Configure kubectl:**
   Ensure your kubectl is configured to access your Kubernetes cluster:
   ```bash
   kubectl cluster-info
   ```

3. **Plan and Apply:**
   ```bash
   terraform plan
   terraform apply
   ```

## Notes

- All examples assume you have a working Kubernetes cluster and kubectl configured
- Some examples use variables that you'll need to define in your root module
- Examples are meant to be educational - adapt them to your specific needs
- The `_examples` directory contains legacy examples that have been migrated here

## Documentation Generation

These examples are used by `terraform-plugin-docs` to generate provider documentation.

To regenerate documentation:
```bash
go generate ./...
```

This will update the `docs/` directory with the latest resource and data source documentation.
