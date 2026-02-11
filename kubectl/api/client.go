package api

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/alekc/terraform-provider-kubectl/kubectl/yaml"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	apiMachineryTypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/cli-runtime/pkg/printers"
	k8sresource "k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubectl/pkg/cmd/apply"
	k8sdelete "k8s.io/kubectl/pkg/cmd/delete"
	"k8s.io/kubectl/pkg/scheme"
	"k8s.io/kubectl/pkg/validation"
)

// RestClientResult contains the result of creating a REST client.
type RestClientResult struct {
	ResourceInterface dynamic.ResourceInterface
	Error             error
}

// RestClientResultSuccess creates a successful result.
func RestClientResultSuccess(ri dynamic.ResourceInterface) *RestClientResult {
	return &RestClientResult{ResourceInterface: ri}
}

// RestClientResultFromErr creates an error result.
func RestClientResultFromErr(err error) *RestClientResult {
	return &RestClientResult{Error: err}
}

// RestClientResultFromInvalidTypeErr creates an invalid type error result.
func RestClientResultFromInvalidTypeErr(err error) *RestClientResult {
	return &RestClientResult{Error: err}
}

// RestClientGetter implements genericclioptions.RESTClientGetter.
type RestClientGetter struct {
	restConfig *restclient.Config
}

// NewRestClientGetter creates a new REST client getter.
func NewRestClientGetter(config *restclient.Config) *RestClientGetter {
	return &RestClientGetter{restConfig: config}
}

// ToRESTConfig returns the REST config.
func (r *RestClientGetter) ToRESTConfig() (*restclient.Config, error) {
	return r.restConfig, nil
}

// ToDiscoveryClient returns a cached discovery client.
func (r *RestClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	discoveryClient := discovery.NewDiscoveryClientForConfigOrDie(r.restConfig)
	return memory.NewMemCacheClient(discoveryClient), nil
}

// ToRESTMapper returns a REST mapper.
func (r *RestClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := r.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	if discoveryClient != nil {
		mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
		expander := restmapper.NewShortcutExpander(mapper, discoveryClient, func(msg string) {
			// Log warnings silently
		})
		return expander, nil
	}
	return nil, fmt.Errorf("no restmapper available")
}

// ToRawKubeConfigLoader returns a kubeconfig loader.
func (r *RestClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return nil
}

// GetRestClientFromUnstructured creates a dynamic client for the given manifest.
// Adapted from SDK v2 kubernetes/resource_kubectl_manifest.go.
func GetRestClientFromUnstructured(
	ctx context.Context,
	manifest *yaml.Manifest,
	clientset *kubernetes.Clientset,
	restConfig *restclient.Config,
) *RestClientResult {
	doGetRestClient := func() *RestClientResult {
		// Use the k8s Discovery service to find all valid APIs for this cluster
		discoveryClient := clientset.Discovery()
		_, resources, err := discoveryClient.ServerGroupsAndResources()

		// There is a partial failure mode here where not all groups are returned
		// We'll try to continue if it's a GroupDiscoveryFailedError
		if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
			return RestClientResultFromErr(err)
		}

		// Validate that the APIVersion provided in the YAML is valid for this cluster
		apiResource, exists := CheckAPIResourceIsPresent(resources, *manifest.Raw)
		if !exists {
			// API not found, try invalidating cache and retrying
			// This handles the case when a CRD is being created by another resource
			_, resources, err = discoveryClient.ServerGroupsAndResources()

			if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
				return RestClientResultFromErr(err)
			}

			// Check for resource again
			apiResource, exists = CheckAPIResourceIsPresent(resources, *manifest.Raw)
			if !exists {
				return RestClientResultFromInvalidTypeErr(
					fmt.Errorf(
						"resource [%s/%s] isn't valid for cluster, check the APIVersion and Kind fields are valid",
						manifest.Raw.GroupVersionKind().GroupVersion().String(),
						manifest.GetKind(),
					),
				)
			}
		}

		resourceStruct := k8sschema.GroupVersionResource{
			Group:    apiResource.Group,
			Version:  apiResource.Version,
			Resource: apiResource.Name,
		}

		// For core services (ServiceAccount, Service etc) the group is incorrectly parsed.
		// "v1" should be empty group and "v1" for version
		if resourceStruct.Group == "v1" && resourceStruct.Version == "" {
			resourceStruct.Group = ""
			resourceStruct.Version = "v1"
		}

		// Get dynamic client based on the found resource struct
		client := dynamic.NewForConfigOrDie(restConfig).Resource(resourceStruct)

		// If the resource is namespaced and doesn't have a namespace defined, set it to default
		if apiResource.Namespaced {
			if !manifest.HasNamespace() {
				manifest.SetNamespace("default")
			}
			return RestClientResultSuccess(client.Namespace(manifest.GetNamespace()))
		}

		return RestClientResultSuccess(client)
	}

	// Run with timeout
	discoveryWithTimeout := func() <-chan *RestClientResult {
		ch := make(chan *RestClientResult)
		go func() {
			ch <- doGetRestClient()
		}()
		return ch
	}

	timeout := time.NewTimer(60 * time.Second)
	defer timeout.Stop()

	select {
	case res := <-discoveryWithTimeout():
		return res
	case <-timeout.C:
		log.Printf("[ERROR] %v timed out fetching resources from discovery client", manifest)
		return RestClientResultFromErr(
			fmt.Errorf("%v timed out fetching resources from discovery client", manifest),
		)
	}
}

// CheckAPIResourceIsPresent loops through available APIResources and
// checks if there is a resource for the APIVersion and Kind defined in the resource.
func CheckAPIResourceIsPresent(
	available []*meta_v1.APIResourceList,
	resource meta_v1_unstruct.Unstructured,
) (*meta_v1.APIResource, bool) {
	resourceGroupVersionKind := resource.GroupVersionKind()
	for _, rList := range available {
		if rList == nil {
			continue
		}
		group := rList.GroupVersion
		for _, r := range rList.APIResources {
			if group == resourceGroupVersionKind.GroupVersion().String() &&
				r.Kind == resource.GetKind() {
				r.Group = resourceGroupVersionKind.Group
				r.Version = resourceGroupVersionKind.Version
				r.Kind = resourceGroupVersionKind.Kind
				return &r, true
			}
		}
	}
	log.Printf(
		"[ERROR] Could not find a valid ApiResource for manifest %s/%s/%s",
		resourceGroupVersionKind.Group,
		resourceGroupVersionKind.Version,
		resourceGroupVersionKind.Kind,
	)
	return nil, false
}

// NewApplyOptions creates apply options for kubectl apply.
// Adapted from SDK v2 kubernetes/resource_kubectl_manifest.go.
func NewApplyOptions(yamlBody string, restConfig *restclient.Config) *apply.ApplyOptions {
	applyOptions := &apply.ApplyOptions{
		PrintFlags: genericclioptions.NewPrintFlags("created").WithTypeSetter(scheme.Scheme),

		IOStreams: genericiooptions.IOStreams{
			In:     strings.NewReader(yamlBody),
			Out:    log.Writer(),
			ErrOut: log.Writer(),
		},

		Overwrite:    true,
		OpenAPIPatch: true,
		Recorder:     genericclioptions.NoopRecorder{},

		VisitedUids:       sets.New[apiMachineryTypes.UID](),
		VisitedNamespaces: sets.New[string](),
	}

	applyOptions.Builder = k8sresource.NewBuilder(NewRestClientGetter(restConfig))

	return applyOptions
}

// ConfigureApplyOptions configures apply options with common settings.
func ConfigureApplyOptions(
	opts *apply.ApplyOptions,
	manifest *yaml.Manifest,
	filename string,
	validateSchema bool,
	serverSideApply bool,
	fieldManager string,
	forceConflicts bool,
) {
	opts.DeleteOptions = &k8sdelete.DeleteOptions{
		FilenameOptions: k8sresource.FilenameOptions{
			Filenames: []string{filename},
		},
	}

	opts.ToPrinter = func(string) (printers.ResourcePrinter, error) {
		return printers.NewDiscardingPrinter(), nil
	}

	if !validateSchema {
		opts.Validator = validation.NullSchema{}
	}

	if serverSideApply {
		opts.ServerSideApply = true
		opts.FieldManager = fieldManager
	}

	if forceConflicts {
		opts.ForceConflicts = true
	}

	if manifest.HasNamespace() {
		opts.Namespace = manifest.GetNamespace()
	}
}

// IsNotFoundError checks if an error is a Kubernetes NotFound error.
func IsNotFoundError(err error) bool {
	return errors.IsNotFound(err) || errors.IsGone(err)
}

// MapRemoveNulls recursively removes null values from a map.
// This is needed because Kubernetes API doesn't accept null values.
func MapRemoveNulls(in map[string]any) map[string]any {
	for k, v := range in {
		switch tv := v.(type) {
		case []any:
			in[k] = SliceRemoveNulls(tv)
		case map[string]any:
			in[k] = MapRemoveNulls(tv)
		default:
			if v == nil {
				delete(in, k)
			}
		}
	}
	return in
}

// SliceRemoveNulls recursively removes null values from a slice.
func SliceRemoveNulls(in []any) []any {
	s := []any{}
	for _, v := range in {
		switch tv := v.(type) {
		case []any:
			s = append(s, SliceRemoveNulls(tv))
		case map[string]any:
			s = append(s, MapRemoveNulls(tv))
		default:
			if v != nil {
				s = append(s, v)
			}
		}
	}
	return s
}
