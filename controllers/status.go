package controllers

import (
	"context"
	"fmt"
	"os"

	srov1beta1 "github.com/openshift-psap/special-resource-operator/api/v1beta1"
	"github.com/openshift-psap/special-resource-operator/pkg/clients"
	"github.com/openshift-psap/special-resource-operator/pkg/color"
	"github.com/openshift-psap/special-resource-operator/pkg/warn"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	client "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Operator Status
func operatorStatusUpdate(sr *srov1beta1.SpecialResource, state string) {

	update := srov1beta1.SpecialResource{}

	// If we cannot find the SR than something bad is going on ..
	objectKey := types.NamespacedName{Name: sr.GetName(), Namespace: sr.GetNamespace()}
	err := clients.Interface.Get(context.TODO(), objectKey, &update)
	if err != nil {
		warn.OnError(errors.Wrap(err, "Is SR being deleted? Cannot get current instance"))
		return
	}

	update.Status.State = state
	update.DeepCopyInto(sr)

	err = clients.Interface.StatusUpdate(context.TODO(), sr)
	if apierrors.IsConflict(err) {
		objectKey := types.NamespacedName{Name: sr.Name, Namespace: ""}
		err := clients.Interface.Get(context.TODO(), objectKey, sr)
		if apierrors.IsNotFound(err) {
			return
		}
		// Do not update the status if we're in the process of being deleted
		isMarkedToBeDeleted := sr.GetDeletionTimestamp() != nil
		if isMarkedToBeDeleted {
			return
		}

	}

	if err != nil {
		log.Error(err, "Failed to update SpecialResource status")
		return
	}
}

// ClusterOperator Status ------------------------------------------------------
func (r *SpecialResourceReconciler) clusterOperatorStatusGetOrCreate() error {

	clusterOperators := &configv1.ClusterOperatorList{}

	opts := []client.ListOption{}

	if err := clients.Interface.List(context.TODO(), clusterOperators, opts...); err != nil {
		return err
	}

	for _, clusterOperator := range clusterOperators.Items {
		if clusterOperator.GetName() == r.GetName() {
			clusterOperator.DeepCopyInto(&r.clusterOperator)
			return nil
		}
	}

	// If we land here there is no clusteroperator object for SRO, create it.
	log = r.Log.WithName(color.Print("status", color.Blue))
	log.Info("No ClusterOperator found... Creating ClusterOperator for SRO")

	co := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: r.GetName()}}

	co, err := clients.Interface.ClusterOperatorCreate(context.TODO(), co, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create ClusterOperator %s: %w", co.Name, err)
	}

	co.DeepCopyInto(&r.clusterOperator)

	return nil
}

func (r *SpecialResourceReconciler) clusterOperatorStatusSet() error {

	if releaseVersion := os.Getenv("RELEASE_VERSION"); len(releaseVersion) > 0 {
		operatorv1helpers.SetOperandVersion(&r.clusterOperator.Status.Versions, configv1.OperandVersion{Name: "operator", Version: releaseVersion})
	}
	return nil
}

func (r *SpecialResourceReconciler) clusterOperatorStatusReconcile(
	conditions []configv1.ClusterOperatorStatusCondition) error {
	// First get the latest clusterOperator before changing anything
	if err := r.clusterOperatorGetLatest(); err != nil {
		return fmt.Errorf("failed to update clusterOperator with latest from API server: %w", err)
	}

	r.clusterOperator.Status.Conditions = conditions

	if err := r.clusterOperatorUpdateRelatedObjects(); err != nil {
		return fmt.Errorf("cannot set ClusterOperator related objects: %w", err)
	}

	if err := r.clusterOperatorStatusSet(); err != nil {
		return fmt.Errorf("cannot update the ClusterOperator status: %w", err)
	}

	if err := r.clusterOperatorStatusUpdate(); err != nil {
		return fmt.Errorf("could not update ClusterOperator: %w", err)
	}

	return nil
}

func (r *SpecialResourceReconciler) clusterOperatorGetLatest() error {
	co, err := clients.Interface.ClusterOperatorGet(context.TODO(), r.GetName(), metav1.GetOptions{})
	co.DeepCopyInto(&r.clusterOperator)
	return err
}

func (r *SpecialResourceReconciler) clusterOperatorStatusUpdate() error {

	if _, err := clients.Interface.ClusterOperatorUpdateStatus(context.TODO(), &r.clusterOperator, metav1.UpdateOptions{}); err != nil {
		return err
	}
	return nil
}

func (r *SpecialResourceReconciler) clusterOperatorUpdateRelatedObjects() error {
	relatedObjects := []configv1.ObjectReference{
		{Group: "", Resource: "namespaces", Name: "openshift-special-resource-operator"},
		{Group: "sro.openshift.io", Resource: "specialresources", Name: ""},
	}

	// Get all specialresource objects
	specialresources := &srov1beta1.SpecialResourceList{}
	err := clients.Interface.List(context.TODO(), specialresources, []client.ListOption{}...)
	if err != nil {
		return err
	}

	//Add namespace for each specialresource to related objects
	for _, sr := range specialresources.Items {
		if sr.Spec.Namespace != "" { // preamble specialresource has no namespace
			log.Info("Adding to relatedObjects", "namespace", sr.Spec.Namespace)
			relatedObjects = append(relatedObjects, configv1.ObjectReference{Group: "", Resource: "namespaces", Name: sr.Spec.Namespace})
		}
	}

	r.clusterOperator.Status.RelatedObjects = relatedObjects

	return nil
}

// SpecialResourcesStatusInit Depending on what error we're getting from the
// reconciliation loop we're updating the status
// nil -> All things good and default conditions can be applied
func SpecialResourcesStatus(r *SpecialResourceReconciler, req ctrl.Request, cond []configv1.ClusterOperatorStatusCondition) (ctrl.Result, error) {
	log = r.Log.WithName(color.Print("status", color.Blue))

	clusterOperatorAvailable, err := clients.Interface.HasResource(configv1.SchemeGroupVersion.WithResource("clusteroperators"))

	if err != nil {
		return reconcile.Result{Requeue: true}, fmt.Errorf("Cannot discover ClusterOperator api resource: %w", err)
	}

	if clusterOperatorAvailable {
		// If clusterOperator CRD does not exist, warn and return nil,
		if err = r.clusterOperatorStatusGetOrCreate(); err != nil {
			return reconcile.Result{Requeue: true}, fmt.Errorf("Cannot get or create ClusterOperator: %w", err)
		}

		log.Info("Reconciling ClusterOperator")
		if err = r.clusterOperatorStatusReconcile(cond); err != nil {
			return reconcile.Result{Requeue: true}, fmt.Errorf("Reconciling ClusterOperator failed: %w", err)
		}
	} else {
		log.Info("Warning: ClusterOperator resource not available. Can be ignored on vanilla k8s.")
	}

	return reconcile.Result{}, nil
}
