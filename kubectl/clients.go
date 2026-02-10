// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"fmt"
	"time"

	"github.com/alekc/terraform-provider-kubectl/kubectl/api"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// getDynamicClient returns a configured unstructured (dynamic) client instance.
func (p *kubectlProviderData) getDynamicClient() (dynamic.Interface, error) {
	if p.RestConfig == nil {
		return nil, fmt.Errorf("cannot create dynamic client: no client config")
	}

	return p.dynamicClient.Get(func() (dynamic.Interface, error) {
		return dynamic.NewForConfig(p.RestConfig)
	})
}

// getDiscoveryClient returns a configured discovery client instance.
func (p *kubectlProviderData) getDiscoveryClient() (discovery.DiscoveryInterface, error) {
	if p.RestConfig == nil {
		return nil, fmt.Errorf("cannot create discovery client: no client config")
	}

	return p.discoveryClient.Get(func() (discovery.DiscoveryInterface, error) {
		return discovery.NewDiscoveryClientForConfig(p.RestConfig)
	})
}

// getRestMapper returns a RESTMapper client instance using in-memory cache.
func (p *kubectlProviderData) getRestMapper() (meta.RESTMapper, error) {
	return p.restMapper.Get(func() (meta.RESTMapper, error) {
		dc, err := p.getDiscoveryClient()
		if err != nil {
			return nil, err
		}

		cacheClient := memory.NewMemCacheClient(dc)
		return restmapper.NewDeferredDiscoveryRESTMapper(cacheClient), nil
	})
}

// getRestClient returns a raw REST client instance.
func (p *kubectlProviderData) getRestClient() (rest.Interface, error) {
	if p.RestConfig == nil {
		return nil, fmt.Errorf("cannot create REST client: no client config")
	}

	return p.restClient.Get(func() (rest.Interface, error) {
		return rest.UnversionedRESTClientFor(p.RestConfig)
	})
}

// getOAPIv2Foundry returns an interface to request tftype types from an OpenAPIv2 spec.
func (p *kubectlProviderData) getOAPIv2Foundry() (api.Foundry, error) {
	return p.OAPIFoundry.Get(func() (api.Foundry, error) {
		rc, err := p.getRestClient()
		if err != nil {
			return nil, fmt.Errorf("failed get OpenAPI spec: %s", err)
		}

		rq := rc.Verb("GET").Timeout(30 * time.Second).AbsPath("openapi", "v2")
		rs, err := rq.DoRaw(context.TODO())
		if err != nil {
			return nil, fmt.Errorf("failed get OpenAPI spec: %s", err)
		}

		oapif, err := api.NewFoundryFromSpecV2(rs)
		if err != nil {
			return nil, fmt.Errorf("failed construct OpenAPI foundry: %s", err)
		}

		return oapif, nil
	})
}
