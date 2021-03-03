/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	"context"

	v1beta1 "github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeClusterServiceDescriptions implements ClusterServiceDescriptionInterface
type FakeClusterServiceDescriptions struct {
	Fake *FakeServicecatalogV1beta1
}

var clusterservicedescriptionsResource = schema.GroupVersionResource{Group: "servicecatalog.k8s.io", Version: "v1beta1", Resource: "clusterservicedescriptions"}

var clusterservicedescriptionsKind = schema.GroupVersionKind{Group: "servicecatalog.k8s.io", Version: "v1beta1", Kind: "ClusterServiceDescription"}

// Get takes name of the clusterServiceDescription, and returns the corresponding clusterServiceDescription object, and an error if there is any.
func (c *FakeClusterServiceDescriptions) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1beta1.ClusterServiceDescription, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootGetAction(clusterservicedescriptionsResource, name), &v1beta1.ClusterServiceDescription{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.ClusterServiceDescription), err
}

// List takes label and field selectors, and returns the list of ClusterServiceDescriptions that match those selectors.
func (c *FakeClusterServiceDescriptions) List(ctx context.Context, opts v1.ListOptions) (result *v1beta1.ClusterServiceDescriptionList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootListAction(clusterservicedescriptionsResource, clusterservicedescriptionsKind, opts), &v1beta1.ClusterServiceDescriptionList{})
	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1beta1.ClusterServiceDescriptionList{ListMeta: obj.(*v1beta1.ClusterServiceDescriptionList).ListMeta}
	for _, item := range obj.(*v1beta1.ClusterServiceDescriptionList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested clusterServiceDescriptions.
func (c *FakeClusterServiceDescriptions) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchAction(clusterservicedescriptionsResource, opts))
}

// Create takes the representation of a clusterServiceDescription and creates it.  Returns the server's representation of the clusterServiceDescription, and an error, if there is any.
func (c *FakeClusterServiceDescriptions) Create(ctx context.Context, clusterServiceDescription *v1beta1.ClusterServiceDescription, opts v1.CreateOptions) (result *v1beta1.ClusterServiceDescription, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootCreateAction(clusterservicedescriptionsResource, clusterServiceDescription), &v1beta1.ClusterServiceDescription{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.ClusterServiceDescription), err
}

// Update takes the representation of a clusterServiceDescription and updates it. Returns the server's representation of the clusterServiceDescription, and an error, if there is any.
func (c *FakeClusterServiceDescriptions) Update(ctx context.Context, clusterServiceDescription *v1beta1.ClusterServiceDescription, opts v1.UpdateOptions) (result *v1beta1.ClusterServiceDescription, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateAction(clusterservicedescriptionsResource, clusterServiceDescription), &v1beta1.ClusterServiceDescription{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.ClusterServiceDescription), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeClusterServiceDescriptions) UpdateStatus(ctx context.Context, clusterServiceDescription *v1beta1.ClusterServiceDescription, opts v1.UpdateOptions) (*v1beta1.ClusterServiceDescription, error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateSubresourceAction(clusterservicedescriptionsResource, "status", clusterServiceDescription), &v1beta1.ClusterServiceDescription{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.ClusterServiceDescription), err
}

// Delete takes name of the clusterServiceDescription and deletes it. Returns an error if one occurs.
func (c *FakeClusterServiceDescriptions) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewRootDeleteAction(clusterservicedescriptionsResource, name), &v1beta1.ClusterServiceDescription{})
	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeClusterServiceDescriptions) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewRootDeleteCollectionAction(clusterservicedescriptionsResource, listOpts)

	_, err := c.Fake.Invokes(action, &v1beta1.ClusterServiceDescriptionList{})
	return err
}

// Patch applies the patch and returns the patched clusterServiceDescription.
func (c *FakeClusterServiceDescriptions) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1beta1.ClusterServiceDescription, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootPatchSubresourceAction(clusterservicedescriptionsResource, name, pt, data, subresources...), &v1beta1.ClusterServiceDescription{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.ClusterServiceDescription), err
}