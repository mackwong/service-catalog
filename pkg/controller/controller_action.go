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
	"fmt"
	osb "github.com/kubernetes-sigs/go-open-service-broker-client/v2"
	"github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
	"github.com/kubernetes-sigs/service-catalog/pkg/pretty"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"net"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

const (
	errorActionCallReason              string = "ActionCallFailed"
	errorServiceActionOrphanMitigation string = "ServiceActionNeedsOrphanMitigation"

	asyncActionReason    string = "Acting"
	asyncActionMessage   string = "The action is being created asynchronously"
	asyncUnactionReason  string = "Unaction"
	asyncUnactionMessage string = "The action is being deleted asynchronously"

	successUnactionReason string = "UnactionSuccessfully"
)

// Cluster service plan handlers and control-loop

func (c *controller) serviceActionAdd(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.Errorf("ClusterServiceDescription: Couldn't get key for object %+v: %v", obj, err)
		return
	}
	c.serviceActionQueue.Add(key)
}

func (c *controller) serviceActionUpdate(oldObj, newObj interface{}) {
	// Action with ongoing asynchronous operations will be manually added
	// to the polling queue by the reconciler. They should be ignored here in
	// order to enforce polling rate-limiting.
	serviceAction := newObj.(*v1beta1.ServiceAction)
	if !serviceAction.Status.AsyncOpInProgress {
		c.serviceActionAdd(newObj)
	}
}

func (c *controller) serviceActionDelete(obj interface{}) {
	serviceAction, ok := obj.(*v1beta1.ServiceAction)
	if serviceAction == nil || !ok {
		return
	}

	klog.V(4).Infof("ClusterServiceAction: Received delete event for %v; no further processing will occur", serviceAction.Name)
}

// reconcileClusterServicePlanKey reconciles a ClusterServicePlan due to resync
// or an event on the ClusterServicePlan.  Note that this is NOT the main
// reconciliation loop for ClusterServicePlans. ClusterServicePlans are
// primarily reconciled in a separate flow when a ClusterServiceBroker is
// reconciled.
func (c *controller) reconcileServiceActionKey(key string) error {

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}
	pcb := pretty.NewContextBuilder(pretty.ServiceAction, namespace, name, "")
	action, err := c.serviceActionLister.ServiceActions(namespace).Get(name)
	if apierrors.IsNotFound(err) {
		klog.Info(pcb.Message("Not doing work because the ServiceActions has been deleted"))
		return nil
	}
	if err != nil {
		klog.Info(pcb.Messagef("Unable to retrieve store: %v", err))
		return err
	}

	return c.reconcileServiceAction(action)
}

// getReconciliationActionForServiceBinding gets the action the reconciler
// should be taking on the given binding.
func getReconciliationActionForServiceAction(action *v1beta1.ServiceAction) ReconciliationAction {
	switch {
	case action.Status.AsyncOpInProgress:
		return reconcilePoll
	case action.ObjectMeta.DeletionTimestamp != nil || action.Status.OrphanMitigationInProgress:
		return reconcileDelete
	default:
		return reconcileAdd
	}
}

func (c *controller) reconcileServiceAction(action *v1beta1.ServiceAction) error {
	pcb := pretty.NewActionContextBuilder(action)
	klog.V(6).Info(pcb.Messagef(`beginning to process resourceVersion: %v`, action.ResourceVersion))

	reconciliationAction := getReconciliationActionForServiceAction(action)
	switch reconciliationAction {
	case reconcileAdd:
		return c.reconcileServiceActionAdd(action)
	case reconcileDelete:
		return c.reconcileServiceActionDelete(action)
	case reconcilePoll:
		return c.pollServiceAction(action)
	default:
		return fmt.Errorf(pcb.Messagef("Unknown reconciliation action %v", reconciliationAction))
	}
}

func (c *controller) reconcileServiceActionAdd(action *v1beta1.ServiceAction) error {
	pcb := pretty.NewActionContextBuilder(action)

	if !c.isServiceActionStatusInitialized(action) {
		klog.V(4).Info(pcb.Message("Initialize Status entry"))
		if err := c.initializeServiceActionStatus(action); err != nil {
			klog.Errorf(pcb.Messagef("Error initializing status: %v", err))
			return err
		}
		return nil
	}

	if isServiceActionFailed(action) {
		klog.V(4).Info(pcb.Message("not processing event; status showed that it has failed"))
		return nil
	}

	//if binding.Status.ReconciledGeneration == binding.Generation {
	//	klog.V(4).Info(pcb.Message("Not processing event; reconciled generation showed there is no work to do"))
	//	return nil
	//}

	klog.V(4).Info(pcb.Message("Processing"))

	action = action.DeepCopy()

	instance, err := c.instanceLister.ServiceInstances(action.Namespace).Get(action.Spec.InstanceRef.Name)
	if err != nil {
		msg := fmt.Sprintf(`References a non-existent %s "%s/%s"`, pretty.ServiceInstance, action.Namespace, action.Spec.InstanceRef.Name)
		readyCond := newServiceActionReadyCondition(v1beta1.ConditionFalse, errorNonexistentServiceInstanceReason, msg)
		return c.processServiceActionOperationError(action, readyCond)
	}

	var prettyName string
	var brokerClient osb.Client
	var request *osb.ActionRequest

	if instance.Spec.ClusterServiceClassSpecified() {
		if instance.Spec.ClusterServiceClassRef == nil || instance.Spec.ClusterServicePlanRef == nil {
			// retry later
			msg := fmt.Sprintf(`Action cannot begin because ClusterServiceClass and ClusterServicePlan references for %s have not been resolved yet`, pretty.ServiceInstanceName(instance))
			readyCond := newServiceActionReadyCondition(v1beta1.ConditionFalse, errorServiceInstanceRefsUnresolved, msg)
			return c.processServiceActionOperationError(action, readyCond)
		}

		serviceClass, _, brokerName, bClient, err := c.getClusterServiceClassPlanAndClusterServiceBrokerForServiceAction(instance, action)
		if err != nil {
			return c.handleServiceActionReconciliationError(action, err)
		}

		brokerClient = bClient

		if !isServiceInstanceReady(instance) {
			msg := fmt.Sprintf(`Binding cannot begin because referenced %s is not ready`, pretty.ServiceInstanceName(instance))
			readyCond := newServiceActionReadyCondition(v1beta1.ConditionFalse, errorServiceInstanceNotReadyReason, msg)
			return c.processServiceActionOperationError(action, readyCond)
		}

		klog.V(4).Info(pcb.Message("Adding/Updating"))

		request, err = c.prepareActionRequest(action, instance)
		if err != nil {
			return c.handleServiceActionReconciliationError(action, err)
		}

		prettyName = pretty.FromServiceInstanceOfClusterServiceClassAtBrokerName(instance, serviceClass, brokerName)
	}

	if action.Status.CurrentOperation == "" {
		action, err = c.recordStartOfServiceActionOperation(action, v1beta1.ServiceActionOperationAction)
		if err != nil {
			// There has been an update to the binding. Start reconciliation
			// over with a fresh view of the binding.
			return err
		}
		// recordStartOfServiceBindingOperation has updated the binding, so we need to continue in the next iteration
		return nil
	}

	response, err := brokerClient.Act(request)
	if err != nil {
		if httpErr, ok := osb.IsHTTPError(err); ok {
			msg := fmt.Sprintf("ServiceBroker returned failure; action operation will not be retried: %v", err.Error())
			readyCond := newServiceActionReadyCondition(v1beta1.ConditionFalse, errorActionCallReason, msg)
			failedCond := newServiceActionFailedCondition(v1beta1.ConditionTrue, "ServiceActionReturnedFailure", msg)
			return c.processActionFailure(action, readyCond, failedCond, shouldStartOrphanMitigation(httpErr.StatusCode))
		}

		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			msg := "Communication with the ServiceBroker timed out; Bind operation will not be retried: " + err.Error()
			failedCond := newServiceActionFailedCondition(v1beta1.ConditionTrue, errorActionCallReason, msg)
			return c.processActionFailure(action, nil, failedCond, true)
		}

		msg := fmt.Sprintf(`Error creating ServiceBinding for %s: %s`, prettyName, err)
		readyCond := newServiceActionReadyCondition(v1beta1.ConditionFalse, errorActionCallReason, msg)

		if c.reconciliationRetryDurationExceeded(action.Status.OperationStartTime) {
			msg := "Stopping reconciliation retries, too much time has elapsed"
			failedCond := newServiceActionFailedCondition(v1beta1.ConditionTrue, errorReconciliationRetryTimeoutReason, msg)
			return c.processActionFailure(action, readyCond, failedCond, false)
		}

		return c.processServiceActionOperationError(action, readyCond)
	}

	if response.Async {
		return c.processActionAsyncResponse(action, response)
	}

	return nil
}

func (c *controller) reconcileServiceActionDelete(action *v1beta1.ServiceAction) error {
	var err error
	pcb := pretty.NewActionContextBuilder(action)

	if action.DeletionTimestamp == nil && !action.Status.OrphanMitigationInProgress {
		// nothing to do...
		return nil
	}

	if finalizers := sets.NewString(action.Finalizers...); !finalizers.Has(v1beta1.FinalizerServiceCatalog) {
		return nil
	}

	klog.V(4).Info(pcb.Message("Processing Delete"))

	action = action.DeepCopy()

	if err := c.ejectServiceAction(action); err != nil {
		msg := fmt.Sprintf(`Error ejecting action. Error deleting secret: %s`, err)
		readyCond := newServiceActionReadyCondition(v1beta1.ConditionFalse, errorEjectingBindReason, msg)
		return c.processServiceActionOperationError(action, readyCond)
	}

	if action.DeletionTimestamp == nil {
		if action.Status.OperationStartTime == nil {
			now := metav1.Now()
			action.Status.OperationStartTime = &now
		}
	} else {
		if action.Status.CurrentOperation != v1beta1.ServiceActionOperationUnAction {
			action, err = c.recordStartOfServiceActionOperation(action, v1beta1.ServiceActionOperationUnAction)
			if err != nil {
				// There has been an update to the action. Start reconciliation
				// over with a fresh view of the action.
				return err
			}
			// recordStartOfServiceBindingOperation has updated the action, so we need to continue in the next iteration
			return nil
		}
	}

	instance, err := c.instanceLister.ServiceInstances(action.Namespace).Get(action.Spec.InstanceRef.Name)
	if err != nil {
		msg := fmt.Sprintf(
			`References a non-existent %s "%s/%s"`,
			pretty.ServiceInstance, action.Namespace, action.Spec.InstanceRef.Name,
		)
		readyCond := newServiceActionReadyCondition(v1beta1.ConditionFalse, errorNonexistentServiceInstanceReason, msg)
		return c.processServiceActionOperationError(action, readyCond)
	}

	if instance.Status.AsyncOpInProgress {
		msg := fmt.Sprintf(
			`trying to unbind to %s "%s/%s" that has ongoing asynchronous operation`,
			pretty.ServiceInstance, action.Namespace, action.Spec.InstanceRef.Name,
		)
		readyCond := newServiceActionReadyCondition(v1beta1.ConditionFalse, errorWithOngoingAsyncOperationReason, msg)
		return c.processServiceActionOperationError(action, readyCond)
	}

	var brokerClient osb.Client
	var prettyBrokerName string

	if instance.Spec.ClusterServiceClassSpecified() {

		if instance.Spec.ClusterServiceClassRef == nil {
			return fmt.Errorf("ClusterServiceClass reference for Instance has not been resolved yet")
		}
		if instance.Status.ExternalProperties == nil || instance.Status.ExternalProperties.ClusterServicePlanExternalID == "" {
			return fmt.Errorf("ClusterServicePlanExternalID for Instance has not been set yet")
		}

		serviceClass, brokerName, bClient, err := c.getClusterServiceClassAndClusterServiceBrokerForServiceAction(instance, action)
		if err != nil {
			return c.handleServiceActionReconciliationError(action, err)
		}

		brokerClient = bClient
		prettyBrokerName = pretty.FromServiceInstanceOfClusterServiceClassAtBrokerName(instance, serviceClass, brokerName)

	}

	request, err := c.prepareUnactionRequest(action, instance)
	if err != nil {
		return c.handleServiceActionReconciliationError(action, err)
	}

	response, err := brokerClient.Unact(request)
	if err != nil {
		msg := fmt.Sprintf(
			`Error unaction from %s: %s`, prettyBrokerName, err,
		)
		readyCond := newServiceActionReadyCondition(v1beta1.ConditionUnknown, errorUnbindCallReason, msg)

		if c.reconciliationRetryDurationExceeded(action.Status.OperationStartTime) {
			msg := "Stopping reconciliation retries, too much time has elapsed"
			failedCond := newServiceActionReadyCondition(v1beta1.ConditionTrue, errorReconciliationRetryTimeoutReason, msg)
			return c.processUnactionFailure(action, readyCond, failedCond)
		}

		return c.processServiceActionOperationError(action, readyCond)
	}

	if response.Async {
		return c.processUnactionAsyncResponse(action, response)
	}

	return c.processUnactionSuccess(action)
}

func (c *controller) pollServiceAction(action *v1beta1.ServiceAction) error {
	return nil
}

func (c *controller) isServiceActionStatusInitialized(action *v1beta1.ServiceAction) bool {
	emptyStatus := v1beta1.ServiceActionStatus{}
	return !reflect.DeepEqual(action.Status, emptyStatus)
}

// handleServiceActionReconciliationError is a helper function that handles on
// error whether the error represents an operation error and should update the
// ServiceBinding resource.
func (c *controller) handleServiceActionReconciliationError(action *v1beta1.ServiceAction, err error) error {
	if resourceErr, ok := err.(*operationError); ok {
		readyCond := newServiceActionReadyCondition(v1beta1.ConditionFalse, resourceErr.reason, resourceErr.message)
		return c.processServiceActionOperationError(action, readyCond)
	}
	return err
}

// initializeServiceActionStatus initialize the ServiceActionStatus.
// In normal scenario it should be done when client is creating the ServiceActions,
// but right now we cannot modify the Status (sub-resource) in webhook on CREATE action.
// As a temporary solution we are doing that in the reconcile function.
func (c *controller) initializeServiceActionStatus(action *v1beta1.ServiceAction) error {
	updated := action.DeepCopy()
	updated.Status = v1beta1.ServiceActionStatus{
		Conditions: []v1beta1.ServiceActionCondition{},
	}

	_, err := c.serviceCatalogClient.ServiceActions(updated.Namespace).UpdateStatus(context.Background(), updated, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func isServiceActionFailed(action *v1beta1.ServiceAction) bool {
	for _, condition := range action.Status.Conditions {
		if condition.Type == v1beta1.ServiceActionConditionFailed && condition.Status == v1beta1.ConditionTrue {
			return true
		}
	}
	return false
}

// newServiceActionReadyCondition is a helper function that returns a Ready
// condition with the given status, reason, and message, with its transition
// time set to now.
func newServiceActionReadyCondition(status v1beta1.ConditionStatus, reason, message string) *v1beta1.ServiceActionCondition {
	return &v1beta1.ServiceActionCondition{
		Type:               v1beta1.ServiceActionConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
}

func newServiceActionFailedCondition(status v1beta1.ConditionStatus, reason, message string) *v1beta1.ServiceActionCondition {
	return &v1beta1.ServiceActionCondition{
		Type:               v1beta1.ServiceActionConditionFailed,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
}

// processServiceActionOperationError handles the logging and updating of a
// ServiceBinding that hit a retryable error during reconciliation.
func (c *controller) processServiceActionOperationError(action *v1beta1.ServiceAction, readyCond *v1beta1.ServiceActionCondition) error {
	c.recorder.Event(action, corev1.EventTypeWarning, readyCond.Reason, readyCond.Message)
	setServiceActionCondition(action, readyCond.Type, readyCond.Status, readyCond.Reason, readyCond.Message)
	if _, err := c.updateServiceActionStatus(action); err != nil {
		return err
	}

	return fmt.Errorf(readyCond.Message)
}

// setServiceActionCondition sets a single condition on a ServiceBinding's
// status: if the condition already exists in the status, it is mutated; if the
// condition does not already exist in the status, it is added. Other
// conditions in the // status are not altered. If the condition exists and its
// status changes, the LastTransitionTime field is updated.

//
// Note: objects coming from informers should never be mutated; always pass a
// deep copy as the binding parameter.
func setServiceActionCondition(toUpdate *v1beta1.ServiceAction,
	conditionType v1beta1.ServiceActionConditionType,
	status v1beta1.ConditionStatus,
	reason, message string) {

	setServiceActionConditionInternal(toUpdate, conditionType, status, reason, message, metav1.Now())
	toUpdate.RecalculatePrinterColumnStatusFields()
}

// setServiceBindingConditionInternal is
// setServiceActionCondition but allows the time to be parameterized
// for testing.
func setServiceActionConditionInternal(toUpdate *v1beta1.ServiceAction,
	conditionType v1beta1.ServiceActionConditionType,
	status v1beta1.ConditionStatus,
	reason, message string,
	t metav1.Time) {
	pcb := pretty.NewActionContextBuilder(toUpdate)
	klog.Info(pcb.Message(message))
	klog.V(5).Info(pcb.Messagef(
		"Setting condition %q to %v",
		conditionType, status,
	))

	newCondition := v1beta1.ServiceActionCondition{
		Type:    conditionType,
		Status:  status,
		Reason:  reason,
		Message: message,
	}

	if len(toUpdate.Status.Conditions) == 0 {
		klog.Info(pcb.Messagef(
			"Setting lastTransitionTime for condition %q to %v",
			conditionType, t,
		))
		newCondition.LastTransitionTime = t
		toUpdate.Status.Conditions = []v1beta1.ServiceActionCondition{newCondition}
		return
	}
	for i, cond := range toUpdate.Status.Conditions {
		if cond.Type == conditionType {
			if cond.Status != newCondition.Status {
				klog.V(3).Info(pcb.Messagef(
					"Found status change for condition %q: %q -> %q; setting lastTransitionTime to %v",
					conditionType, cond.Status, status, t,
				))
				newCondition.LastTransitionTime = t
			} else {
				newCondition.LastTransitionTime = cond.LastTransitionTime
			}

			toUpdate.Status.Conditions[i] = newCondition
			return
		}
	}

	klog.V(3).Info(
		pcb.Messagef("Setting lastTransitionTime for condition %q to %v",
			conditionType, t,
		))

	newCondition.LastTransitionTime = t
	toUpdate.Status.Conditions = append(toUpdate.Status.Conditions, newCondition)
}

func (c *controller) updateServiceActionStatus(toUpdate *v1beta1.ServiceAction) (*v1beta1.ServiceAction, error) {
	pcb := pretty.NewActionContextBuilder(toUpdate)
	klog.V(4).Info(pcb.Message("Updating status"))
	updatedAction, err := c.serviceCatalogClient.ServiceActions(toUpdate.Namespace).UpdateStatus(context.Background(), toUpdate, metav1.UpdateOptions{})
	if err != nil {
		klog.Errorf(pcb.Messagef("Error updating status: %v", err))
	} else {
		klog.V(6).Info(pcb.Messagef(`Updated status of resourceVersion: %v; got resourceVersion: %v`,
			toUpdate.ResourceVersion, updatedAction.ResourceVersion),
		)
	}

	return updatedAction, err
}

// prepareActionRequest creates a bind request object to be passed to the broker
// client to create the given binding.
func (c *controller) prepareActionRequest(
	action *v1beta1.ServiceAction, instance *v1beta1.ServiceInstance) (
	*osb.ActionRequest, error) {

	var scExternalID string
	var spExternalID string

	if instance.Spec.ClusterServiceClassSpecified() {

		serviceClass, err := c.getClusterServiceClassForServiceAction(instance, action)
		if err != nil {
			return nil, &operationError{
				reason:  errorNonexistentClusterServiceClassReason,
				message: err.Error(),
			}
		}

		servicePlan, err := c.getClusterServicePlanForServiceAction(instance, action, serviceClass)
		if err != nil {
			return nil, &operationError{
				reason:  errorNonexistentClusterServicePlanReason,
				message: err.Error(),
			}
		}

		scExternalID = serviceClass.Spec.ExternalID
		spExternalID = servicePlan.Spec.ExternalID

	} else if instance.Spec.ServiceClassSpecified() {

		serviceClass, err := c.getClusterServiceClassForServiceAction(instance, action)
		if err != nil {
			return nil, &operationError{
				reason:  errorNonexistentServiceClassReason,
				message: err.Error(),
			}
		}

		servicePlan, err := c.getClusterServicePlanForServiceAction(instance, action, serviceClass)
		if err != nil {
			return nil, &operationError{
				reason:  errorNonexistentServicePlanReason,
				message: err.Error(),
			}
		}

		scExternalID = serviceClass.Spec.ExternalID
		spExternalID = servicePlan.Spec.ExternalID
	}

	ns, err := c.kubeClient.CoreV1().Namespaces().Get(context.Background(), instance.Namespace, metav1.GetOptions{})
	if err != nil {
		return nil, &operationError{
			reason:  errorFindingNamespaceServiceInstanceReason,
			message: fmt.Sprintf(`Failed to get namespace %q during action: %s`, instance.Namespace, err),
		}
	}

	parameters, _, _, err := prepareInProgressPropertyParameters(
		c.kubeClient,
		action.Namespace,
		action.Spec.Parameters,
		action.Spec.ParametersFrom,
	)
	if err != nil {
		return nil, &operationError{
			reason:  errorWithParametersReason,
			message: err.Error(),
		}
	}

	appGUID := string(ns.UID)
	clusterID := c.getClusterID()

	requestContext := map[string]interface{}{
		"platform":           ContextProfilePlatformKubernetes,
		"namespace":          instance.Namespace,
		clusterIdentifierKey: clusterID,
		"instance_name":      instance.Name,
	}

	request := &osb.ActionRequest{
		ActionID:          action.Spec.ExternalID,
		InstanceID:        instance.Spec.ExternalID,
		ServiceID:         scExternalID,
		PlanID:            spExternalID,
		AppGUID:           &appGUID,
		Parameters:        parameters,
		BindResource:      &osb.BindResource{AppGUID: &appGUID},
		Context:           requestContext,
		AcceptsIncomplete: true,
	}

	return request, nil
}

// updateServiceActionCondition updates the given condition for the given ServiceBinding
// with the given status, reason, and message.
func (c *controller) updateServiceActionCondition(
	action *v1beta1.ServiceAction,
	conditionType v1beta1.ServiceActionConditionType,
	status v1beta1.ConditionStatus,
	reason, message string) error {

	pcb := pretty.NewActionContextBuilder(action)
	toUpdate := action.DeepCopy()

	setServiceActionCondition(toUpdate, conditionType, status, reason, message)

	klog.V(4).Info(pcb.Messagef(
		"Updating %v condition to %v (Reason: %q, Message: %q)",
		conditionType, status, reason, message,
	))
	_, err := c.serviceCatalogClient.ServiceActions(action.Namespace).UpdateStatus(context.Background(), toUpdate, metav1.UpdateOptions{})
	if err != nil {
		klog.Errorf(pcb.Messagef(
			"Error updating %v condition to %v: %v",
			conditionType, status, err,
		))
	}
	return err
}

// recordStartOfServiceActionOperation updates the binding to indicate
// that there is a current operation being performed. The Status of the binding
// is recorded in the registry.
// params:
// toUpdate - a modifiable copy of the binding in the registry to update
// operation - operation that is being performed on the binding
// inProgressProperties - the new properties, if any, to apply to the binding
// returns:
// 1 - a modifiable copy of toUpdate; or toUpdate if there was an error
// 2 - any error that occurred
func (c *controller) recordStartOfServiceActionOperation(
	toUpdate *v1beta1.ServiceAction, operation v1beta1.ServiceActionOperation) (
	*v1beta1.ServiceAction, error) {

	clearServiceActionCurrentOperation(toUpdate)

	toUpdate.Status.CurrentOperation = operation
	now := metav1.Now()
	toUpdate.Status.OperationStartTime = &now
	reason := ""
	message := ""
	switch operation {
	case v1beta1.ServiceActionOperationAction:
		reason = bindingInFlightReason
		message = bindingInFlightMessage
	case v1beta1.ServiceActionOperationUnAction:
		reason = unbindingInFlightReason
		message = unbindingInFlightMessage
	}
	setServiceActionCondition(
		toUpdate,
		v1beta1.ServiceActionConditionReady,
		v1beta1.ConditionFalse,
		reason,
		message,
	)
	return c.updateServiceActionStatus(toUpdate)
}

// clearServiceActionCurrentOperation sets the fields of the binding's
// Status to indicate that there is no current operation being performed. The
// Status is *not* recorded in the registry.
func clearServiceActionCurrentOperation(toUpdate *v1beta1.ServiceAction) {
	toUpdate.Status.CurrentOperation = ""
	toUpdate.Status.OperationStartTime = nil
	toUpdate.Status.AsyncOpInProgress = false
	toUpdate.Status.LastOperation = nil
	toUpdate.Status.OrphanMitigationInProgress = false
}

// processActionFailure handles the logging and updating of a ServiceBinding that
// hit a terminal failure during bind reconciliation.
func (c *controller) processActionFailure(action *v1beta1.ServiceAction, readyCond, failedCond *v1beta1.ServiceActionCondition, shouldMitigateOrphan bool) error {
	//currentReconciledGeneration := action.Status.ReconciledGeneration
	if readyCond != nil {
		c.recorder.Event(action, corev1.EventTypeWarning, readyCond.Reason, readyCond.Message)
		setServiceActionCondition(action, readyCond.Type, readyCond.Status, readyCond.Reason, readyCond.Message)
	}

	c.recorder.Event(action, corev1.EventTypeWarning, failedCond.Reason, failedCond.Message)
	setServiceActionCondition(action, failedCond.Type, failedCond.Status, failedCond.Reason, failedCond.Message)

	if shouldMitigateOrphan {
		msg := "Starting orphan mitigation"
		readyCond := newServiceActionReadyCondition(v1beta1.ConditionFalse, errorServiceActionOrphanMitigation, msg)
		setServiceActionCondition(action, readyCond.Type, readyCond.Status, readyCond.Reason, readyCond.Message)
		c.recorder.Event(action, corev1.EventTypeWarning, readyCond.Reason, readyCond.Message)

		action.Status.OrphanMitigationInProgress = true
		action.Status.AsyncOpInProgress = false
		action.Status.OperationStartTime = nil
	} else {
		clearServiceActionCurrentOperation(action)
		//rollbackBindingReconciledGenerationOnDeletion(action, currentReconciledGeneration)
	}

	if _, err := c.updateServiceActionStatus(action); err != nil {
		return err
	}

	return nil
}

// processBindAsyncResponse handles the logging and updating of a
// ServiceInstance that received an asynchronous response from the broker when
// requesting a bind.
func (c *controller) processActionAsyncResponse(action *v1beta1.ServiceAction, response *osb.ActionResponse) error {
	setServiceActionLastOperation(action, response.OperationKey)
	setServiceActionCondition(action, v1beta1.ServiceActionConditionReady, v1beta1.ConditionFalse, asyncActionReason, asyncActionMessage)
	action.Status.AsyncOpInProgress = true

	if _, err := c.updateServiceActionStatus(action); err != nil {
		return err
	}

	c.recorder.Event(action, corev1.EventTypeNormal, asyncActionReason, asyncActionMessage)
	return c.beginPollingServiceAction(action)
}

// setServiceBindingLastOperation sets the last operation key on the given
// binding.
func setServiceActionLastOperation(action *v1beta1.ServiceAction, operationKey *osb.OperationKey) {
	if operationKey != nil && *operationKey != "" {
		key := string(*operationKey)
		action.Status.LastOperation = &key
	}
}

// beginPollingServiceBinding does a rate-limited add of the key for the given
// binding to the controller's binding polling queue.
func (c *controller) beginPollingServiceAction(action *v1beta1.ServiceAction) error {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(action)
	if err != nil {
		klog.Errorf("Couldn't create a key for object %+v: %v", action, err)
		return fmt.Errorf("Couldn't create a key for object %+v: %v", action, err)
	}

	c.actionPollingQueue.AddRateLimited(key)

	return nil
}

func (c *controller) requeueServiceActionForPoll(key string) error {
	c.serviceActionQueue.Add(key)
	return nil
}

func (c *controller) ejectServiceAction(action *v1beta1.ServiceAction) error {
	var err error
	pcb := pretty.NewActionContextBuilder(action)
	klog.V(5).Info(pcb.Messagef(`Deleting Secret "%s/%s"`,
		action.Namespace, action.Spec.SecretName,
	))

	if err = c.kubeClient.CoreV1().Secrets(action.Namespace).Delete(context.Background(), action.Spec.SecretName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	return nil
}

func (c *controller) prepareUnactionRequest(
	action *v1beta1.ServiceAction, instance *v1beta1.ServiceInstance) (
	*osb.UnactionRequest, error) {

	var scExternalID string
	var planExternalID string

	if instance.Spec.ClusterServiceClassSpecified() {

		serviceClass, err := c.getClusterServiceClassForServiceAction(instance, action)
		if err != nil {
			return nil, c.handleServiceActionReconciliationError(action, err)
		}

		scExternalID = serviceClass.Spec.ExternalID
		planExternalID = instance.Status.ExternalProperties.ClusterServicePlanExternalID

	}

	request := &osb.UnactionRequest{
		ActionID:          action.Spec.ExternalID,
		InstanceID:        instance.Spec.ExternalID,
		ServiceID:         scExternalID,
		PlanID:            planExternalID,
		AcceptsIncomplete: true,
	}
	return request, nil
}

// processUnactionFailure handles the logging and updating of a
// ServiceBinding that hit a terminal failure during unbind
// reconciliation.
func (c *controller) processUnactionFailure(action *v1beta1.ServiceAction, readyCond, failedCond *v1beta1.ServiceActionCondition) error {
	if failedCond == nil {
		return fmt.Errorf("failedCond must not be nil")
	}

	if readyCond != nil {
		setServiceActionCondition(action, v1beta1.ServiceActionConditionReady, v1beta1.ConditionUnknown, readyCond.Reason, readyCond.Message)
		c.recorder.Event(action, corev1.EventTypeWarning, readyCond.Reason, readyCond.Message)
	}

	if action.Status.OrphanMitigationInProgress {
		// replace Ready condition with orphan mitigation-related one.
		msg := "Orphan mitigation failed: " + failedCond.Message
		readyCond := newServiceActionReadyCondition(v1beta1.ConditionUnknown, errorOrphanMitigationFailedReason, msg)
		setServiceActionCondition(action, v1beta1.ServiceActionConditionReady, readyCond.Status, readyCond.Reason, readyCond.Message)
		c.recorder.Event(action, corev1.EventTypeWarning, readyCond.Reason, readyCond.Message)
	} else {
		setServiceActionCondition(action, v1beta1.ServiceActionConditionFailed, failedCond.Status, failedCond.Reason, failedCond.Message)
		c.recorder.Event(action, corev1.EventTypeWarning, failedCond.Reason, failedCond.Message)
	}

	clearServiceActionCurrentOperation(action)
	//action.Status.UnbindStatus = v1beta1.ServiceBindingUnbindStatusFailed

	if _, err := c.updateServiceActionStatus(action); err != nil {
		return err
	}

	return nil
}

// ServiceBinding that received an asynchronous response from the broker when
// requesting an unbind.
func (c *controller) processUnactionAsyncResponse(action *v1beta1.ServiceAction, response *osb.UnactionResponse) error {
	setServiceActionLastOperation(action, response.OperationKey)
	setServiceActionCondition(action, v1beta1.ServiceActionConditionReady, v1beta1.ConditionFalse, asyncUnactionReason, asyncUnactionMessage)
	action.Status.AsyncOpInProgress = true

	if _, err := c.updateServiceActionStatus(action); err != nil {
		return err
	}

	c.recorder.Event(action, corev1.EventTypeNormal, asyncUnactionReason, asyncUnactionMessage)
	return c.beginPollingServiceAction(action)
}

// processUnbindSuccess handles the logging and updating of a ServiceBinding
// that has successfully been deleted at the broker.
func (c *controller) processUnactionSuccess(action *v1beta1.ServiceAction) error {
	mitigatingOrphan := action.Status.OrphanMitigationInProgress

	reason := successUnactionReason
	msg := "The action was deleted successfully"
	if mitigatingOrphan {
		reason = successOrphanMitigationReason
		msg = successOrphanMitigationMessage
	}

	setServiceActionCondition(action, v1beta1.ServiceActionConditionReady, v1beta1.ConditionFalse, reason, msg)
	clearServiceActionCurrentOperation(action)
	//action.Status.UnbindStatus = v1beta1.ServiceBindingUnbindStatusSucceeded

	if mitigatingOrphan {
		if _, err := c.updateServiceActionStatus(action); err != nil {
			return err
		}
	} else {
		// If part of a resource deletion request, follow-through to
		// the graceful deletion handler in order to clear the finalizer.
		if err := c.processServiceActionGracefulDeletionSuccess(action); err != nil {
			return err
		}
	}

	c.recorder.Event(action, corev1.EventTypeNormal, reason, msg)
	return nil
}

// processServiceBindingGracefulDeletionSuccess handles the logging and
// updating of a ServiceBinding that has successfully finished graceful
// deletion.
func (c *controller) processServiceActionGracefulDeletionSuccess(action *v1beta1.ServiceAction) error {
	pcb := pretty.NewActionContextBuilder(action)

	updatedBinding, err := c.updateServiceActionStatus(action)
	if err != nil {
		return fmt.Errorf("while updating status: %v", err)
	}
	klog.Info(pcb.Message("Status updated"))

	toUpdate := updatedBinding.DeepCopy()
	finalizers := sets.NewString(toUpdate.Finalizers...)
	finalizers.Delete(v1beta1.FinalizerServiceCatalog)
	toUpdate.Finalizers = finalizers.List()

	_, err = c.serviceCatalogClient.ServiceActions(toUpdate.Namespace).Update(context.Background(), toUpdate, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("while removing finalizer entry: %v", err)
	}
	klog.Info(pcb.Message("Cleared finalizer"))

	return nil

}
