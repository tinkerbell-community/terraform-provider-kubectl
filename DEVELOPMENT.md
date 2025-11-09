# Development Guide

## Local Development Setup

When developing the kubectl provider locally, you'll want to use a locally built binary without version conflicts. Terraform provides a mechanism called **dev_overrides** for this purpose.

### Using dev_overrides

1. **Build the provider:**
   ```bash
   make build
   # Or manually:
   go build -o terraform-provider-kubectl
   ```

2. **Create or edit `~/.terraformrc`:**
   ```hcl
   provider_installation {
     dev_overrides {
       "alekc/kubectl" = "/Users/atkini01/src/appkins/terraform-provider-kubectl"
     }

     # For all other providers, use the registry
     direct {}
   }
   ```
   
   **Important:** Replace the path with your actual workspace directory.

3. **Use Terraform as normal:**
   ```bash
   cd your-terraform-project
   terraform init
   terraform plan
   terraform apply
   ```

   Terraform will use your local binary and bypass version checking.

### Important Notes

- **Dev Override Warning:** Terraform will show a warning that you're using a dev override. This is expected:
  ```
  Warning: Provider development overrides are in effect
  ```

- **State Compatibility:** Your state file will **not** record the provider version when using dev overrides.

- **Remove for Production:** When you're done developing, comment out or remove the `dev_overrides` block from `~/.terraformrc`.

### Alternative: Building with Explicit Version

If you need to test with a specific version (matching your state):

```bash
# Build with specific version
go build -ldflags "-X main.version=v2.0.3" -o terraform-provider-kubectl

# Install to Terraform's plugin directory
mkdir -p ~/.terraform.d/plugins/registry.terraform.io/alekc/kubectl/2.0.3/darwin_arm64
cp terraform-provider-kubectl ~/.terraform.d/plugins/registry.terraform.io/alekc/kubectl/2.0.3/darwin_arm64/

# Run terraform init to use the new version
cd your-terraform-project
rm -rf .terraform .terraform.lock.hcl
terraform init
```

**Note:** Adjust `darwin_arm64` to match your platform (e.g., `darwin_amd64`, `linux_amd64`).

## Version Management

The provider version is injected at build time via ldflags:

```bash
# Makefile uses git describe
make build
# Results in: LDFLAGS += -X main.version=$(git describe --always --abbrev=40 --dirty)

# Manual version override
go build -ldflags "-X main.version=v2.0.3"
```

### Versioning Workflow

1. **During Development:** Use dev_overrides (no version needed)
2. **For Testing:** Build with explicit version matching your test state
3. **For Release:** Create a git tag, then build:
   ```bash
   git tag v2.0.4
   git push origin v2.0.4
   make build
   ```

## Testing

### Acceptance Tests

```bash
# Set up environment
export TF_ACC=1
export KUBECONFIG=~/.kube/config

# Run all tests
make testacc

# Run specific test
go test -v ./kubectl -run TestAccManifestResource_basic
```

### Manual Testing

1. Build the provider: `make build`
2. Set up dev_overrides in `~/.terraformrc`
3. Create a test configuration in a new directory:
   ```hcl
   terraform {
     required_providers {
       kubectl = {
         source = "alekc/kubectl"
       }
     }
   }

   provider "kubectl" {
     config_path = "~/.kube/config"
   }

   resource "kubectl_manifest" "test" {
     yaml_body = <<-YAML
       apiVersion: v1
       kind: ConfigMap
       metadata:
         name: test-config
       data:
         key: value
     YAML
   }
   ```
4. Run: `terraform init && terraform plan && terraform apply`

## Documentation Generation

The provider uses terraform-plugin-docs for documentation generation:

```bash
# Generate documentation
go generate ./...

# Or manually
go tool github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate -provider-name kubectl
```

This reads examples from the `examples/` directory and generates documentation in `docs/`.

## Common Issues

### "Resource instance managed by newer provider version"

**Cause:** Your state file has a provider version that doesn't match your local binary.

**Solution:** Use dev_overrides in `~/.terraformrc` (see above).

### "Provider not found"

**Cause:** Terraform can't locate your local binary.

**Solution:** 
1. Ensure the path in `dev_overrides` is correct
2. Ensure you've built the provider: `make build`
3. Check the binary exists: `ls -la terraform-provider-kubectl`

### "Could not load plugin"

**Cause:** Binary architecture mismatch.

**Solution:** Build for your platform: `GOOS=darwin GOARCH=arm64 go build`

## Release Process

1. **Update Version:**
   - Create a git tag: `git tag v2.0.4`
   - Push the tag: `git push origin v2.0.4`

2. **Build Release Binaries:**
   ```bash
   make cross-compile
   ```

3. **Update Documentation:**
   ```bash
   go generate ./...
   git add docs/
   git commit -m "Update documentation for v2.0.4"
   ```

4. **Create GitHub Release:**
   - Use the tag created in step 1
   - Attach binaries from `out/` directory
   - Include changelog

5. **Publish to Registry:**
   - Follow [Terraform Registry publishing guide](https://developer.hashicorp.com/terraform/registry/providers/publishing)
   - Ensure GPG signing is configured
