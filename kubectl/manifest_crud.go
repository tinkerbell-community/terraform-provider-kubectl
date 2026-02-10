// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"fmt"
	"log"

	"github.com/alekc/terraform-provider-kubectl/kubectl/api"
	"github.com/alekc/terraform-provider-kubectl/yaml"
	"k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// applyManifestV2 applies a Kubernetes manifest using the new Dynamic attribute schema.
// This replaces the old yaml_body-based applyManifest method.
func (r *manifestResource) applyManifestV2(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	// Build unstructured object from Dynamic attributes
	uo, diags := buildUnstructured(
		ctx,
		model.APIVersion.ValueString(),
		model.Kind.ValueString(),
		model.Metadata,
		model.Spec,
	)
	if diags.HasError() {
		return fmt.Errorf("failed to build unstructured: %v", diags)
	}

	log.Printf("[DEBUG] Applying Kubernetes resource: %s/%s", uo.GetKind(), uo.GetName())

	// Get field manager configuration
	fieldManagerName := "Terraform"
	forceConflicts := false

	if !model.FieldManager.IsNull() {
		var fmModels []fieldManagerModel
		diags := model.FieldManager.ElementsAs(ctx, &fmModels, false)
		if diags.HasError() {
			return fmt.Errorf("failed to parse field_manager: %v", diags)
		}
		if len(fmModels) > 0 {
			if !fmModels[0].Name.IsNull() {
				fieldManagerName = fmModels[0].Name.ValueString()
			}
			if !fmModels[0].ForceConflicts.IsNull() {
				forceConflicts = fmModels[0].ForceConflicts.ValueBool()
			}
		}
	}

	// Create REST client for this resource type
	manifest := yaml.NewFromUnstructured(uo)
	restClient := api.GetRestClientFromUnstructured(
		ctx,
		manifest,
		r.providerData.MainClientset,
		r.providerData.RestConfig,
	)
	if restClient.Error != nil {
		return fmt.Errorf("failed to create kubernetes rest client: %w", restClient.Error)
	}

	// Remove nulls from the object before applying
	content := uo.UnstructuredContent()
	cleanedContent := api.MapRemoveNulls(content)
	uo.SetUnstructuredContent(cleanedContent)

	// Marshal to JSON for server-side apply
	jsonData, err := uo.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal to JSON: %w", err)
	}

	// Apply using server-side apply (Patch with ApplyPatchType)
	result, err := restClient.ResourceInterface.Patch(
		ctx,
		uo.GetName(),
		types.ApplyPatchType,
		jsonData,
		meta_v1.PatchOptions{
			FieldManager: fieldManagerName,
			Force:        &forceConflicts,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to apply manifest: %w", err)
	}

	log.Printf("[DEBUG] Successfully applied resource: %s/%s (UID: %s)",
		result.GetKind(), result.GetName(), result.GetUID())

	return nil
}

// readManifestV2 reads a Kubernetes resource and populates the state using Dynamic attributes.
func (r *manifestResource) readManifestV2(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	// Extract name and namespace from metadata
	name, err := extractMetadataField(ctx, model.Metadata, "name")
	if err != nil || name == "" {
		return fmt.Errorf("failed to extract name from metadata: %w", err)
	}

	namespace, _ := extractMetadataField(ctx, model.Metadata, "namespace")

	log.Printf("[DEBUG] Reading Kubernetes resource: %s/%s (namespace: %s)",
		model.Kind.ValueString(), name, namespace)

	// Create REST client
	// Build a minimal unstructured object for the REST client
	tempUo := &meta_v1_unstruct.Unstructured{}
	tempUo.SetAPIVersion(model.APIVersion.ValueString())
	tempUo.SetKind(model.Kind.ValueString())
	tempUo.SetName(name)
	if namespace != "" {
		tempUo.SetNamespace(namespace)
	}

	manifest := yaml.NewFromUnstructured(tempUo)
	restClient := api.GetRestClientFromUnstructured(
		ctx,
		manifest,
		r.providerData.MainClientset,
		r.providerData.RestConfig,
	)
	if restClient.Error != nil {
		return fmt.Errorf("failed to create kubernetes rest client: %w", restClient.Error)
	}

	// Get the resource from Kubernetes
	result, err := restClient.ResourceInterface.Get(
		ctx,
		name,
		meta_v1.GetOptions{},
	)
	if err != nil {
		return err
	}

	log.Printf("[DEBUG] Successfully read resource: %s/%s (UID: %s)",
		result.GetKind(), result.GetName(), result.GetUID())

	// Populate state from the resource
	diags := setStateFromUnstructured(ctx, result, model)
	if diags.HasError() {
		return fmt.Errorf("failed to set state: %v", diags)
	}

	return nil
}

// deleteManifestV2 deletes a Kubernetes resource using Dynamic attributes.
func (r *manifestResource) deleteManifestV2(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	// Extract name and namespace from metadata
	name, err := extractMetadataField(ctx, model.Metadata, "name")
	if err != nil || name == "" {
		return fmt.Errorf("failed to extract name from metadata: %w", err)
	}

	namespace, _ := extractMetadataField(ctx, model.Metadata, "namespace")

	log.Printf("[DEBUG] Deleting Kubernetes resource: %s/%s (namespace: %s)",
		model.Kind.ValueString(), name, namespace)

	// Build minimal unstructured for REST client
	uo := &meta_v1_unstruct.Unstructured{}
	uo.SetAPIVersion(model.APIVersion.ValueString())
	uo.SetKind(model.Kind.ValueString())
	uo.SetName(name)
	if namespace != "" {
		uo.SetNamespace(namespace)
	}

	manifest := yaml.NewFromUnstructured(uo)
	restClient := api.GetRestClientFromUnstructured(
		ctx,
		manifest,
		r.providerData.MainClientset,
		r.providerData.RestConfig,
	)
	if restClient.Error != nil {
		return fmt.Errorf("failed to create kubernetes rest client: %w", restClient.Error)
	}

	// Determine delete propagation policy
	propagationPolicy := meta_v1.DeletePropagationBackground
	if !model.DeleteCascade.IsNull() {
		switch model.DeleteCascade.ValueString() {
		case string(meta_v1.DeletePropagationForeground):
			propagationPolicy = meta_v1.DeletePropagationForeground
		case string(meta_v1.DeletePropagationBackground):
			propagationPolicy = meta_v1.DeletePropagationBackground
		}
	}

	// Delete the resource
	err = restClient.ResourceInterface.Delete(
		ctx,
		name,
		meta_v1.DeleteOptions{
			PropagationPolicy: &propagationPolicy,
		},
	)

	// Ignore NotFound errors (resource already deleted)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete manifest: %w", err)
	}

	if errors.IsNotFound(err) {
		log.Printf("[DEBUG] Resource already deleted: %s/%s", model.Kind.ValueString(), name)
	} else {
		log.Printf("[DEBUG] Successfully deleted resource: %s/%s", model.Kind.ValueString(), name)
	}

	return nil
}

// mapRemoveNulls and sliceRemoveNulls have been moved to api.MapRemoveNulls and api.SliceRemoveNulls.
