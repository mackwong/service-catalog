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

// Code generated by lister-gen. DO NOT EDIT.

package v1beta1

import (
	v1beta1 "github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// ServiceActionLister helps list ServiceActions.
type ServiceActionLister interface {
	// List lists all ServiceActions in the indexer.
	List(selector labels.Selector) (ret []*v1beta1.ServiceAction, err error)
	// ServiceActions returns an object that can list and get ServiceActions.
	ServiceActions(namespace string) ServiceActionNamespaceLister
	ServiceActionListerExpansion
}

// serviceActionLister implements the ServiceActionLister interface.
type serviceActionLister struct {
	indexer cache.Indexer
}

// NewServiceActionLister returns a new ServiceActionLister.
func NewServiceActionLister(indexer cache.Indexer) ServiceActionLister {
	return &serviceActionLister{indexer: indexer}
}

// List lists all ServiceActions in the indexer.
func (s *serviceActionLister) List(selector labels.Selector) (ret []*v1beta1.ServiceAction, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.ServiceAction))
	})
	return ret, err
}

// ServiceActions returns an object that can list and get ServiceActions.
func (s *serviceActionLister) ServiceActions(namespace string) ServiceActionNamespaceLister {
	return serviceActionNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// ServiceActionNamespaceLister helps list and get ServiceActions.
type ServiceActionNamespaceLister interface {
	// List lists all ServiceActions in the indexer for a given namespace.
	List(selector labels.Selector) (ret []*v1beta1.ServiceAction, err error)
	// Get retrieves the ServiceAction from the indexer for a given namespace and name.
	Get(name string) (*v1beta1.ServiceAction, error)
	ServiceActionNamespaceListerExpansion
}

// serviceActionNamespaceLister implements the ServiceActionNamespaceLister
// interface.
type serviceActionNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all ServiceActions in the indexer for a given namespace.
func (s serviceActionNamespaceLister) List(selector labels.Selector) (ret []*v1beta1.ServiceAction, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.ServiceAction))
	})
	return ret, err
}

// Get retrieves the ServiceAction from the indexer for a given namespace and name.
func (s serviceActionNamespaceLister) Get(name string) (*v1beta1.ServiceAction, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1beta1.Resource("serviceaction"), name)
	}
	return obj.(*v1beta1.ServiceAction), nil
}
