/*


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

package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	airshipv1 "sipcluster/pkg/api/v1"
	airshipsvc "sipcluster/pkg/services"
	airshipvms "sipcluster/pkg/vbmh"
)

const (
	logerName = "sipcluster"
)

// SIPClusterReconciler reconciles a SIPCluster object
type SIPClusterReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=airship.airshipit.org,resources=sipclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=airship.airshipit.org,resources=sipclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=airship.airshipit.org,resources=sipclusters/status,verbs=get;update;patch

// +kubebuilder:rbac:groups="metal3.io",resources=baremetalhosts,verbs=get;update;patch;list

func (r *SIPClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("SIPCluster", req.NamespacedName)
	logger.Info("starting reconciliation cycle")
	ctx := context.Background()
	// Lets retrieve the SIPCluster
	sip := airshipv1.SIPCluster{}
	if err := r.Get(ctx, req.NamespacedName, &sip); err != nil {
		if k8sapierrors.IsNotFound(err) {
			// If not found it may have been deleted by after, doesn' not mean that error happened
			logger.Info("no SIPCluster object found, nothing to do")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Check for Deletion
	// name of our custom finalizer
	sipFinalizerName := "sip.airship.airshipit.org/finalizer"
	// This only works if I add a finalizer to CRD TODO
	if sip.ObjectMeta.DeletionTimestamp.IsZero() {
		logger.Info("SIPcluster object is not deleted trying to gather BMHs for it")
		// machines
		machines, err := r.gatherVBMH(sip)
		if err != nil {
			logger.Info("Failed to gather BaremetalHosts for SIPCluster", "error", err.Error())
			// error is only logged, we don't want reconciliation errors to flood the log because no bmh hosts available
			// TODO Rework gatherVBMH to return error only when real error happens, if no bmh hosts currently available
			// doesn't mean that it is an error, instead controller should simply requeue the request and keep checking
			// for hosts.
			// TODO Investigate on Requeue parameters
			return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
		}

		err = r.deployInfra(sip, machines)
		if err != nil {
			logger.Error(err, "unable to deploy infrastructure services")
			return ctrl.Result{}, err
		}

		err = r.finish(sip, machines)
		if err != nil {
			logger.Error(err, "unable to finish creation/update ..")
			return ctrl.Result{}, err
		}
	} else {
		// Deleting the SIP , what do we do now
		if containsString(sip.ObjectMeta.Finalizers, sipFinalizerName) {
			// our finalizer is present, so lets handle any external dependency
			err := r.finalize(sip)
			if err != nil {
				logger.Error(err, "unable to finalize")
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			sip.ObjectMeta.Finalizers = removeString(sip.ObjectMeta.Finalizers, sipFinalizerName)
			if err := r.Update(context.Background(), &sip); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	return ctrl.Result{}, nil
}

func (r *SIPClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&airshipv1.SIPCluster{}).
		Complete(r)
}

// Helper functions to check and remove string from a slice of strings.
// There might be a golang funuction to do this . Will check later
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

/*
### Gather Phase

#### Identity BMH VM's
- Gather BMH's that meet the criteria expected for the groups
- Check for existing labeled BMH's
- Complete the expected scheduling contraints :
    - If master
        -  collect into list of bmh's to label
    - If worker
        - collect into list of bmh's to label
#### Extract Info from Identified BMH
-  identify and extract  the IP address ands other info as needed (***)
    -  Use it as part of the service infrastucture configuration
- At this point I have a list of BMH's, and I have the extrapolated data I need for configuring services.

### Service Infrastructure Deploy Phase
- Create or Updated the [LB|admin pod] with the appropriate configuration

### Label Phase
- Label the collected hosts.
- At this point SIPCluster is done processing a given CR, and can move on the next.
*/

// machines
func (r *SIPClusterReconciler) gatherVBMH(sip airshipv1.SIPCluster) (*airshipvms.MachineList, error) {
	// TODO consider passing logger as well
	logger := r.Log.WithValues("SIPCluster", sip.GetNamespace()+"/"+sip.GetName())
	// 1- Let me retrieve all BMH  that are unlabeled or already labeled with the target Tenant/CNF
	// 2- Let me now select the one's that meet teh scheduling criteria
	// If I schedule successfully then
	// If Not complete schedule , then throw an error.
	machines := &airshipvms.MachineList{}

	logger.Info("attempting to schedule BaremetalHost objects")
	err := machines.Schedule(sip, r.Client)
	if err != nil {
		return machines, err
	}

	// we extract the information in a generic way
	// So that LB ,  Jump and Ath POD  all leverage the same
	// If there are some issues finnding information the vBMH
	// Are flagged Unschedulable
	// Loop and Try to find new vBMH to complete tge schedule
	if !machines.Extrapolate(sip, r.Client) {
		logger.Info("Could not extrapolate Machine information")
	}
	return machines, nil
}

func (r *SIPClusterReconciler) deployInfra(sip airshipv1.SIPCluster, machines *airshipvms.MachineList) error {
	for sName, sConfig := range sip.Spec.InfraServices {
		// Instantiate
		service, err := airshipsvc.NewService(sName, sConfig)
		if err != nil {
			return err
		}

		// Lets deploy the Service
		err = service.Deploy(machines, r.Client)
		if err != nil {
			return err
		}

		// Did it deploy correctly, letcs check

		err = service.Validate()
		if err != nil {
			return err
		}
	}
	return nil
}

/*
finish shoulld  take care of any wrpa up tasks..
*/
func (r *SIPClusterReconciler) finish(sip airshipv1.SIPCluster, machines *airshipvms.MachineList) error {

	// Label the vBMH's
	err := machines.ApplyLabels(sip, r.Client)
	if err != nil {
		return err
	}
	return nil

}

/**

Deal with Deletion andd Finalizers if any is needed
Such as i'e what are we doing with the lables on teh vBMH's
**/
func (r *SIPClusterReconciler) finalize(sip airshipv1.SIPCluster) error {
	// 1- Let me retrieve all vBMH mapped for this SIP Cluster
	// 2- Let me now select the one's that meet teh scheduling criteria
	// If I schedule successfully then
	// If Not complete schedule , then throw an error.
	machines := &airshipvms.MachineList{}
	fmt.Printf("finalize sip:%v machines:%s\n", sip, machines.String())
	// Update the list of  Machines.
	err := machines.GetCluster(sip, r.Client)
	if err != nil {
		return err
	}
	// Placeholder unsuree whether this is what we want
	err = machines.RemoveLabels(sip, r.Client)
	if err != nil {
		return err
	}
	return nil
}
