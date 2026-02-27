// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/api"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GVRFromUnstructured extracts a canonical schema.GroupVersionResource out of the resource's
// metadata by checking it against the discovery API via a RESTMapper.
func GVRFromUnstructured(
	o *unstructured.Unstructured,
	m meta.RESTMapper,
) (schema.GroupVersionResource, error) {
	apv := o.GetAPIVersion()
	kind := o.GetKind()
	gv, err := schema.ParseGroupVersion(apv)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	mapping, err := m.RESTMapping(gv.WithKind(kind).GroupKind(), gv.Version)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	return mapping.Resource, err
}

// GVKFromTftypesObject extracts a canonical schema.GroupVersionKind out of the resource's
// metadata by checking it against the discovery API via a RESTMapper.
func GVKFromTftypesObject(in *tftypes.Value, m meta.RESTMapper) (schema.GroupVersionKind, error) {
	var obj map[string]tftypes.Value
	err := in.As(&obj)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}
	var apv string
	var kind string
	err = obj["apiVersion"].As(&apv)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}
	err = obj["kind"].As(&kind)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}
	gv, err := schema.ParseGroupVersion(apv)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}
	mappings, err := m.RESTMappings(gv.WithKind(kind).GroupKind())
	if err != nil {
		return schema.GroupVersionKind{}, err
	}
	for _, m := range mappings {
		if m.GroupVersionKind.GroupVersion().String() == apv {
			return m.GroupVersionKind, nil
		}
	}
	return schema.GroupVersionKind{}, errors.New("cannot select exact GV from REST mapper")
}

// IsResourceNamespaced determines if a resource is namespaced or cluster-level
// by querying the Kubernetes discovery API.
func IsResourceNamespaced(gvk schema.GroupVersionKind, m meta.RESTMapper) (bool, error) {
	rm, err := m.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return false, err
	}
	if rm.Scope.Name() == meta.RESTScopeNameNamespace {
		return true, nil
	}
	return false, nil
}

// TFTypeFromOpenAPI generates a tftypes.Type representation of a Kubernetes resource
// designated by the supplied GroupVersionKind resource id.
func (p *kubectlProviderData) TFTypeFromOpenAPI(
	ctx context.Context,
	gvk schema.GroupVersionKind,
	status bool,
) (tftypes.Type, map[string]string, error) {
	var tsch tftypes.Type
	var hints map[string]string

	oapi, err := p.getOAPIv2Foundry()
	if err != nil {
		return nil, hints, fmt.Errorf("cannot get OpenAPI foundry: %s", err)
	}
	// check if GVK is from a CRD
	crdSchema, err := p.lookUpGVKinCRDs(ctx, gvk)
	if err != nil {
		return nil, hints, fmt.Errorf(
			"failed to look up GVK [%s] among available CRDs: %s",
			gvk.String(),
			err,
		)
	}
	if crdSchema != nil {
		crdMap, ok := crdSchema.(map[string]any)
		if !ok {
			return nil, hints, fmt.Errorf("CRD schema is not a map[string]any: %T", crdSchema)
		}
		js, err := json.Marshal(api.SchemaToSpec("", crdMap))
		if err != nil {
			return nil, hints, fmt.Errorf("CRD schema fails to marshal into JSON: %s", err)
		}
		oapiv3, err := api.NewFoundryFromSpecV3(js)
		if err != nil {
			return nil, hints, err
		}
		tsch, hints, err = oapiv3.GetTypeByGVK(gvk)
		if err != nil {
			return nil, hints, fmt.Errorf(
				"failed to generate tftypes for GVK [%s] from CRD schema: %s",
				gvk.String(),
				err,
			)
		}
	}
	if tsch == nil {
		// Not a CRD type - look GVK up in cluster OpenAPI spec
		tsch, hints, err = oapi.GetTypeByGVK(gvk)
		if err != nil {
			return nil, hints, fmt.Errorf(
				"cannot get resource type from OpenAPI (%s): %s",
				gvk.String(),
				err,
			)
		}
	}
	// remove "status" attribute from resource type
	if tsch.Is(tftypes.Object{}) && !status {
		ot, ok := tsch.(tftypes.Object)
		if !ok {
			return nil, hints, fmt.Errorf("resource type is not a tftypes.Object: %T", tsch)
		}
		atts := make(map[string]tftypes.Type)
		for k, t := range ot.AttributeTypes {
			if k != "status" {
				atts[k] = t
			}
		}
		// types from CRDs only contain specific attributes
		// we need to backfill metadata and apiVersion/kind attributes
		if _, ok := atts["apiVersion"]; !ok {
			atts["apiVersion"] = tftypes.String
		}
		if _, ok := atts["kind"]; !ok {
			atts["kind"] = tftypes.String
		}
		metaType, _, err := oapi.GetTypeByGVK(api.ObjectMetaGVK)
		if err != nil {
			return nil, hints, fmt.Errorf("failed to generate tftypes for v1.ObjectMeta: %s", err)
		}
		mo, ok := metaType.(tftypes.Object)
		if !ok {
			return nil, hints, fmt.Errorf("ObjectMeta type is not a tftypes.Object: %T", metaType)
		}
		atts["metadata"] = mo

		tsch = tftypes.Object{AttributeTypes: atts}
	}

	return tsch, hints, nil
}

// RemoveServerSideFields removes certain fields which get added to the
// resource after creation which would cause a perpetual diff.
func RemoveServerSideFields(in map[string]any) map[string]any {
	// Remove "status" attribute
	delete(in, "status")

	if metaRaw, ok := in["metadata"]; ok {
		if meta, ok := metaRaw.(map[string]any); ok {
			// Remove "uid", "creationTimestamp", "resourceVersion" as
			// they change with most resource operations
			delete(meta, "uid")
			delete(meta, "creationTimestamp")
			delete(meta, "resourceVersion")
			delete(meta, "generation")
			delete(meta, "selfLink")

			// TODO: we should be filtering API responses based on the contents of 'managedFields'
			// and only retain the attributes for which the manager is Terraform
			delete(meta, "managedFields")
		}
	}

	return in
}

func (p *kubectlProviderData) lookUpGVKinCRDs(
	ctx context.Context,
	gvk schema.GroupVersionKind,
) (any, error) {
	// check CRD versions
	crds, err := p.fetchCRDs(ctx)
	if err != nil {
		return nil, err
	}

	for _, r := range crds {
		spec, ok := r.Object["spec"].(map[string]any)
		if !ok || spec == nil {
			continue
		}
		grp, _ := spec["group"].(string)
		if grp != gvk.Group {
			continue
		}
		names, ok := spec["names"].(map[string]any)
		if !ok || names == nil {
			continue
		}
		kind, _ := names["kind"].(string)
		if kind != gvk.Kind {
			continue
		}
		ver := spec["versions"]
		if ver == nil {
			ver = spec["version"]
			if ver == nil {
				continue
			}
		}
		verList, ok := ver.([]any)
		if !ok {
			continue
		}
		for _, rv := range verList {
			if rv == nil {
				continue
			}
			v, ok := rv.(map[string]any)
			if !ok {
				continue
			}
			if v["name"] == gvk.Version {
				s, ok := v["schema"].(map[string]any)
				if !ok {
					return nil, nil // non-structural CRD
				}
				return s["openAPIV3Schema"], nil
			}
		}
	}
	return nil, nil
}

func (p *kubectlProviderData) fetchCRDs(ctx context.Context) ([]unstructured.Unstructured, error) {
	return p.crds.Get(func() ([]unstructured.Unstructured, error) {
		c, err := p.getDynamicClient()
		if err != nil {
			return nil, err
		}
		m, err := p.getRestMapper()
		if err != nil {
			return nil, err
		}

		crd := schema.GroupKind{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}
		crms, err := m.RESTMappings(crd)
		if err != nil {
			return nil, fmt.Errorf(
				"could not extract resource version mappings for apiextensions.k8s.io.CustomResourceDefinition: %s",
				err,
			)
		}

		var crds []unstructured.Unstructured
		for _, crm := range crms {
			crdRes, err := c.Resource(crm.Resource).List(ctx, v1.ListOptions{})
			if err != nil {
				return nil, err
			}

			crds = append(crds, crdRes.Items...)
		}

		return crds, nil
	})
}
