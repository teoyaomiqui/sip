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

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	metal3 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	airshipv1 "sipcluster/pkg/api/v1"
	"sipcluster/pkg/vbmh"
	"sipcluster/testutil"
)

var _ = Describe("SIPCluster controller", func() {
	Context("When it detects a SIPCluster", func() {
		It("Should schedule BMHs accordingly", func() {
			By("Labelling nodes")

			// Create vBMH test objects
			nodes := []string{"master", "master", "master", "worker", "worker", "worker", "worker"}
			namespace := "default"
			for node, role := range nodes {
				vBMH, networkData := testutil.CreateBMH(node, 6, namespace, role, true)
				Expect(k8sClient.Create(context.Background(), vBMH)).Should(Succeed())
				Expect(k8sClient.Create(context.Background(), networkData)).Should(Succeed())
			}

			// Create SIP cluster
			clusterName := "subcluster-test1"
			sipCluster := createSIPCluster(clusterName, namespace, 3, 4)
			Expect(k8sClient.Create(context.Background(), sipCluster)).Should(Succeed())

			// Poll BMHs until SIP has scheduled them to the SIP cluster
			Eventually(func() error {
				expectedLabels := map[string]string{
					vbmh.SipScheduleLabel: "true",
					vbmh.SipClusterLabel:  clusterName,
				}

				var bmh metal3.BareMetalHost
				for node := range nodes {
					Expect(k8sClient.Get(context.Background(), types.NamespacedName{
						Name:      fmt.Sprintf("node%d", node),
						Namespace: namespace,
					}, &bmh)).Should(Succeed())
				}

				return compareLabels(expectedLabels, bmh.GetLabels())
			}, 60, 5).Should(Succeed())
		})
	})
})

func compareLabels(expected map[string]string, actual map[string]string) error {
	for k, v := range expected {
		value, exists := actual[k]
		if !exists {
			return fmt.Errorf("label %s=%s missing. Has labels %v", k, v, actual)
		}

		if value != v {
			return fmt.Errorf("label %s=%s does not match expected label %s=%s. Has labels %v", k, value, k,
				v, actual)
		}
	}

	return nil
}

func createSIPCluster(name string, namespace string, masters int, workers int) *airshipv1.SIPCluster {
	return &airshipv1.SIPCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SIPCluster",
			APIVersion: "airship.airshipit.org/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: airshipv1.SIPClusterSpec{
			Config: &airshipv1.SipConfig{
				ClusterName: name,
			},
			Nodes: map[airshipv1.VmRoles]airshipv1.NodeSet{
				airshipv1.VmMaster: airshipv1.NodeSet{
					VmFlavor: "airshipit.org/vino-flavor=master",
					Scheduling: []airshipv1.SchedulingOptions{
						airshipv1.ServerAntiAffinity,
					},
					Count: &airshipv1.VmCount{
						Active:  masters,
						Standby: 0,
					},
				},
				airshipv1.VmWorker: airshipv1.NodeSet{
					VmFlavor: "airshipit.org/vino-flavor=worker",
					Scheduling: []airshipv1.SchedulingOptions{
						airshipv1.ServerAntiAffinity,
					},
					Count: &airshipv1.VmCount{
						Active:  workers,
						Standby: 0,
					},
				},
			},
			InfraServices: map[airshipv1.InfraService]airshipv1.InfraConfig{},
		},
		Status: airshipv1.SIPClusterStatus{},
	}
}
