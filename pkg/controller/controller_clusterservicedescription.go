/*
Copyright 2017 The Kubernetes Authors.

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

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"

	"github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
)

// Cluster service plan handlers and control-loop

func (c *controller) clusterServiceDescriptionAdd(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.Errorf("ClusterServiceDescription: Couldn't get key for object %+v: %v", obj, err)
		return
	}
	c.clusterServiceDescriptionQueue.Add(key)
}

func (c *controller) clusterServiceDescriptionUpdate(oldObj, newObj interface{}) {
	c.clusterServiceDescriptionAdd(newObj)
}

func (c *controller) clusterServiceDescriptionDelete(obj interface{}) {
	clusterServiceDescription, ok := obj.(*v1beta1.ClusterServiceDescription)
	if clusterServiceDescription == nil || !ok {
		return
	}

	klog.V(4).Infof("ClusterServiceDescription: Received delete event for %v; no further processing will occur", clusterServiceDescription.Name)
}

// reconcileClusterServiceDescriptionKey reconciles a ClusterServicePlan due to resync
// or an event on the ClusterServicePlan.  Note that this is NOT the main
// reconciliation loop for ClusterServicePlans. ClusterServicePlans are
// primarily reconciled in a separate flow when a ClusterServiceBroker is
// reconciled.
func (c *controller) reconcileClusterServiceDescriptionKey(key string) error {
	description, err := c.clusterServiceDescriptionLister.Get(key)
	if errors.IsNotFound(err) {
		klog.Infof("clusterServiceDescriptionLister %q: Not doing work because it has been deleted", key)
		return nil
	}
	if err != nil {
		klog.Infof("clusterServiceDescriptionLister %q: Unable to retrieve object from store: %v", key, err)
		return err
	}

	return c.reconcileClusterServiceDescription(description)
}

func (c *controller) reconcileClusterServiceDescription(serviceDescription *v1beta1.ClusterServiceDescription) error {
	klog.Infof("ClusterServiceDescription %q (ExternalName: %q): processing", serviceDescription.Name, serviceDescription.Spec.ExternalName)

	if !serviceDescription.Status.RemovedFromBrokerCatalog {
		return nil
	}

	klog.Infof("ClusterServiceDescription %q (ExternalName: %q): has been removed from broker catalog and has zero instances remaining; deleting", serviceDescription.Name, serviceDescription.Spec.ExternalName)
	return c.serviceCatalogClient.ClusterServiceDescriptions().Delete(context.Background(), serviceDescription.Name, metav1.DeleteOptions{})
}
