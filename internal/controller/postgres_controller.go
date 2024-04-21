/*
Copyright 2024.

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
	"strings"

	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dbv1alpha1 "github.com/janikgar/operator-sandbox/api/v1alpha1"
)

func ptr[T any](d T) *T {
	return &d
}

// PostgresReconciler reconciles a Postgres object
type PostgresReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=db.janikgar.lan,resources=postgres,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=db.janikgar.lan,resources=postgres/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=db.janikgar.lan,resources=postgres/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Postgres object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *PostgresReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var postgres dbv1alpha1.Postgres
	if err := r.Get(ctx, req.NamespacedName, &postgres); err != nil {
		log.Log.Error(err, "could not get Postgres")
		return ctrl.Result{}, client.IgnoreNotFound((err))
	}

	var postgresList dbv1alpha1.PostgresList
	if err := r.List(ctx, &postgresList, client.InNamespace(req.Namespace)); err != nil {
		log.Log.Error(err, "could not list Postgreses")
		return ctrl.Result{}, err
	}

	isPrimary := func(pg *dbv1alpha1.Postgres) bool {
		return pg.Spec.Role == dbv1alpha1.Primary
	}

	getTargetStatus := func(thisPostgres *dbv1alpha1.Postgres) ([]dbv1alpha1.TargetStatus, error) {
		status := []dbv1alpha1.TargetStatus{}
		if len(thisPostgres.Spec.Targets) == 0 {
			return status, nil
		}
		for _, pg := range postgresList.Items {
			for _, claimedReplica := range thisPostgres.Spec.Targets {
				if pg.Name == claimedReplica {
					status = append(status, dbv1alpha1.TargetStatus{
						Name:      pg.Name,
						Available: true,
					})
				}
			}
		}
		if len(status) == len(thisPostgres.Spec.Targets) {
			return nil, fmt.Errorf("found %d replicas of %d", len(status), len(thisPostgres.Spec.Targets))
		}
		return status, nil
	}

	var stsList apps.StatefulSetList

	// selector := labels.NewSelector()
	// req1, err := labels.NewRequirement("managed-by", selection.Equals, []string{"pgoperator"})
	// if err != nil {
	// 	return ctrl.Result{}, err
	// }
	// req2, err := labels.NewRequirement("postgres-name", selection.Equals, []string{postgres.Name})
	// if err != nil {
	// 	return ctrl.Result{}, err
	// }
	// selector.Add(*req1, *req2)
	// // selector := labels.SelectorFromSet(labels.Set{
	// // 	"managed-by":    "pgoperator",
	// // 	"postgres-name": postgres.Name,
	// // })
	if err := r.List(ctx, &stsList, client.InNamespace(req.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	postgres.Status.Available = false
	postgres.Status.Role = dbv1alpha1.Replica
	postgres.Status.Databases = []string{}
	postgres.Status.TargetStatus = []dbv1alpha1.TargetStatus{}

	if len(stsList.Items) == 0 {
		managedObjects := []client.Object{
			postgres.GenerateSA(),
			postgres.GenerateCM(),
			postgres.GenerateSTS(),
		}

		for _, managedObject := range managedObjects {
			if err := r.Create(ctx, managedObject, &client.CreateOptions{FieldManager: "pgoperator"}); err != nil {
				return ctrl.Result{}, fmt.Errorf("could not create object %s/%s: %e", managedObject.GetObjectKind(), managedObject.GetName(), err)
			}
		}
		postgres.Status.Available = true
	}

	if isPrimary(&postgres) {
		postgres.Status.Role = dbv1alpha1.Primary
		targetStatus, err := getTargetStatus(&postgres)
		if err != nil {
			return ctrl.Result{}, err
		}
		postgres.Status.TargetStatus = targetStatus
	}

	if err := r.Status().Update(ctx, &postgres); err != nil {
		log.Log.Error(err, "could not update Postgres status")
		return ctrl.Result{}, err
	}

	finalizerName := "db.janikgar.lan/finalizer"

	if postgres.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&postgres, finalizerName) {
			controllerutil.AddFinalizer(&postgres, finalizerName)
			if err := r.Update(ctx, &postgres); err != nil {
				return ctrl.Result{}, fmt.Errorf("could not add finalizer: %e", err)
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(&postgres, finalizerName) {
			managedObjects := []client.Object{
				postgres.GenerateSA(),
				postgres.GenerateCM(),
				postgres.GenerateSTS(),
			}
			for _, managedObject := range managedObjects {
				var errors []string
				if err := r.Delete(ctx, managedObject); err != nil {
					errors = append(errors, fmt.Sprintf("could not delete object %s/%s: %e", managedObject.GetObjectKind(), managedObject.GetName(), err))
				}
				if len(errors) > 0 {
					return ctrl.Result{}, fmt.Errorf(strings.Join(errors, ";"))
				}
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PostgresReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbv1alpha1.Postgres{}).
		Complete(r)
}
