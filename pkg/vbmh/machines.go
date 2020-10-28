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

package vbmh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	metal3 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	airshipv1 "sipcluster/pkg/api/v1"
	//rbacv1 "k8s.io/api/rbac/v1"
	//"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScheduledState
type ScheduledState string

// Possible Node or VM Roles  for a Tenant
const (
	// ToBeScheduled means that the VM was identified by the scheduler to be selected
	ToBeScheduled ScheduledState = "Selected"

	// Scheduled means the BMH / VM already has a label implying it
	// was previously scheduled
	Scheduled ScheduledState = "Scheduled"

	// NotScheduled, This BMH was not selected to be scheduled.
	// Either because we didnt meet the criteria
	// or simple we reached the count
	NotScheduled ScheduledState = "NotSelected"
)

const (
	BaseAirshipSelector = "airshipit.org"
	SipScheduled        = BaseAirshipSelector + "/sip-scheduled in (True, true)"
	SipNotScheduled     = BaseAirshipSelector + "/sip-scheduled in (False, false)"

	// This is a placeholder . Need to synchronize with ViNO the constants below
	// Probable pll this or eqivakent values from a ViNO pkg
	RackLabel   = BaseAirshipSelector + "/rack"
	ServerLabel = BaseAirshipSelector + "/rack"
)

// MAchine represents an individual BMH CR, and teh appropriate
// attributes required to manage the SIP Cluster scheduling and
// rocesing needs about thhem
type Machine struct {
	Bmh            metal3.BareMetalHost
	ScheduleStatus ScheduledState
	// scheduleLabels
	// I expect to build this over time / if not might not be needed
	ScheduleLabels map[string]string
	VmRole         airshipv1.VmRoles
	// Data will contain whatever information is needed from the server
	// IF it ends up een just the IP then maybe we can collapse into a field
	Data *MachineData
}

type MachineData struct {
	// Collect all IP's for the interfaces defined
	// In the list of Services
	IpOnInterface map[string]string
}

// MachineList contains the list of Scheduled or ToBeScheduled machines
type MachineList struct {
	bmhs []*Machine
}

func (ml *MachineList) Schedule(nodes map[airshipv1.VmRoles]airshipv1.NodeSet, c client.Client) error {
	// Initialize teh Target list
	ml.bmhs = ml.init(nodes)

	// IDentify vBMH's that meet the appropriate selction criteria
	bmList, err := ml.getVBMH(c)
	if err != nil {
		return err
	}

	//  Identify and Select the vBMH I actually will use
	err = ml.identifyNodes(nodes, bmList)
	if err != nil {
		return err
	}

	// If I get here the MachineList should have a selected set of  Machine's
	// They are in the ScheduleStatus of ToBeScheduled as well as the Role
	//

	return nil
}

func (ml *MachineList) init(nodes map[airshipv1.VmRoles]airshipv1.NodeSet) []*Machine {
	mlSize := 0
	for _, nodeCfg := range nodes {
		mlSize = mlSize + nodeCfg.Count.Active + nodeCfg.Count.Standby
	}
	return make([]*Machine, mlSize)

}

func (ml *MachineList) getVBMH(c client.Client) (*metal3.BareMetalHostList, error) {
	bmList := &metal3.BareMetalHostList{}
	// I am thinking we can add a Label for unsccheduled.
	// SIP Cluster can change it to scheduled.
	// We can then simple use this to select UNSCHEDULED
	/*
		This possible will not be needed if I figured out how to provide a != label.
		Then we can use DOESNT HAVE A TENANT LABEL
	*/
	labelSelector := metav1.LabelSelector{MatchLabels: map[string]string{SipNotScheduled: "False"}}
	bmhSelector, err := metav1.LabelSelectorAsSelector(&labelSelector)
	if err != nil {
		return bmList, err
	}
	bmListOptions := &client.ListOptions{
		LabelSelector: bmhSelector,
		Limit:         100,
	}

	err = c.List(context.TODO(), bmList, bmListOptions)
	if err != nil {
		return bmList, err
	}
	return bmList, nil
}

func (ml *MachineList) identifyNodes(nodes map[airshipv1.VmRoles]airshipv1.NodeSet, bmList *metal3.BareMetalHostList) error {
	// If using the SIP Sheduled label, we now have a list of vBMH;'s
	// that are not scheduled
	// Next I need to apply the constraints

	// This willl be a poor mans simple scheduler
	// Only deals with AntiAffinity at :
	// - Racks  : Dont select two machines in the same rack
	// - Server : Dont select two machines in the same server
	for nodeRole, nodeCfg := range nodes {
		scheduleSetMap, err := ml.initScheduleMaps(nodeCfg.Scheduling)
		if err != nil {
			return err
		}
		err = ml.scheduleIt(nodeRole, nodeCfg, bmList, scheduleSetMap)
		if err != nil {
			return err
		}
	}

	return nil
}

func (ml *MachineList) initScheduleMaps(constraints []airshipv1.SchedulingOptions) (map[airshipv1.SchedulingOptions]*ScheduleSet, error) {
	setMap := make(map[airshipv1.SchedulingOptions]*ScheduleSet)

	for _, constraint := range constraints {
		if constraint == airshipv1.RackAntiAffinity {
			setMap[constraint] = &ScheduleSet{
				active:    true,
				set:       make(map[string]bool),
				labelName: RackLabel,
			}
		}
		if constraint == airshipv1.ServerAntiAffinity {
			setMap[constraint] = &ScheduleSet{
				active:    true,
				set:       make(map[string]bool),
				labelName: ServerLabel,
			}
		}
	}

	if len(setMap) > 0 {
		return setMap, ErrorConstraintNotFound{}
	}
	return setMap, nil
}

func (ml *MachineList) scheduleIt(nodeRole airshipv1.VmRoles, nodeCfg airshipv1.NodeSet, bmList *metal3.BareMetalHostList, scheduleSetMap map[airshipv1.SchedulingOptions]*ScheduleSet) error {
	validBmh := true
	nodeTarget := (nodeCfg.Count.Active + nodeCfg.Count.Standby)
	for _, bmh := range bmList.Items {
		for _, constraint := range nodeCfg.Scheduling {
			// Do I care about this constraint

			if scheduleSetMap[constraint].Active() {
				// Check if bmh has the label
				// There is a func (host *BareMetalHost) getLabel(name string) string {
				// Not sure why its not Public, so sing our won method
				cLabelValue, cFlavorMatches := scheduleSetMap[constraint].GetLabels(bmh.Labels, nodeCfg.VmFlavor)

				if cLabelValue != "" && cFlavorMatches {
					// If its in th elist , theen this bmh is disqualified. Skip it
					if scheduleSetMap[constraint].Exists(cLabelValue) {
						validBmh = false
						break
					}
				}
			}
		}
		// All the constraints have been checked
		if validBmh {
			// Lets add it to the list as a schedulable thing
			m := &Machine{
				Bmh:            bmh,
				ScheduleStatus: ToBeScheduled,
				VmRole:         nodeRole,
				Data: &MachineData{
					IpOnInterface: make(map[string]string),
				},
			}
			// Probable need to use the nodeRole as a label here
			ml.bmhs = append(ml.bmhs, m)
			nodeTarget = nodeTarget - 1
			if nodeTarget == 0 {
				break
			}
		}

		// ...
		validBmh = true
	}

	if nodeTarget > 0 {
		return ErrorUnableToFullySchedule{
			TargetNode:   nodeRole,
			TargetFlavor: nodeCfg.VmFlavor,
		}
	}
	return nil
}

// Extrapolate
// The intention is to extract the IP information from the referenced networkData field for the BareMetalHost
func (ml *MachineList) Extrapolate(sip airshipv1.SIPCluster, c client.Client) error {
	// Lets get the data for all selected BMH's.
	for _, machine := range ml.bmhs {
		bmh := machine.Bmh
		// Identify Network Data Secret name

		networkDataSecret := &corev1.Secret{}
		// c is a created client.
		err := c.Get(context.Background(), client.ObjectKey{
			Namespace: bmh.Spec.NetworkData.Namespace,
			Name:      bmh.Spec.NetworkData.Name,
		}, networkDataSecret)
		if err != nil {
			return err
		}

		// Assuming there might be other data
		// Retrieve IP's for Service defined Network Interfaces
		err = ml.getIp(machine, networkDataSecret, sip.Spec.InfraServices)
		if err != nil {
			return err
		}

	}
	return nil
}

/***

{
    "links": [
        {
            "id": "eno4",
            "name": "eno4",
            "type": "phy",
            "mtu": 1500
        },
        {
            "id": "enp59s0f1",
            "name": "enp59s0f1",
            "type": "phy",
            "mtu": 9100
        },
        {
            "id": "enp216s0f0",
            "name": "enp216s0f0",
            "type": "phy",
            "mtu": 9100
        },
        {
            "id": "bond0",
            "name": "bond0",
            "type": "bond",
            "bond_links": [
                "enp59s0f1",
                "enp216s0f0"
            ],
            "bond_mode": "802.3ad",
            "bond_xmit_hash_policy": "layer3+4",
            "bond_miimon": 100,
            "mtu": 9100
        },
        {
            "id": "bond0.41",
            "name": "bond0.41",
            "type": "vlan",
            "vlan_link": "bond0",
            "vlan_id": 41,
            "mtu": 9100,
            "vlan_mac_address": null
        },
        {
            "id": "bond0.42",
            "name": "bond0.42",
            "type": "vlan",
            "vlan_link": "bond0",
            "vlan_id": 42,
            "mtu": 9100,
            "vlan_mac_address": null
        },
        {
            "id": "bond0.44",
            "name": "bond0.44",
            "type": "vlan",
            "vlan_link": "bond0",
            "vlan_id": 44,
            "mtu": 9100,
            "vlan_mac_address": null
        },
        {
            "id": "bond0.45",
            "name": "bond0.45",
            "type": "vlan",
            "vlan_link": "bond0",
            "vlan_id": 45,
            "mtu": 9100,
            "vlan_mac_address": null
        }
    ],
    "networks": [
        {
            "id": "oam-ipv6",
            "type": "ipv6",
            "link": "bond0.41",
            "ip_address": "2001:1890:1001:293d::139",
            "routes": [
                {
                    "network": "::/0",
                    "netmask": "::/0",
                    "gateway": "2001:1890:1001:293d::1"
                }
            ]
        },
        {
            "id": "oam-ipv4",
            "type": "ipv4",
            "link": "bond0.41",
            "ip_address": "32.68.51.139",
            "netmask": "255.255.255.128",
            "dns_nameservers": [
                "135.188.34.124",
                "135.38.244.16",
                "135.188.34.84"
            ],
            "routes": [
                {
                    "network": "0.0.0.0",
                    "netmask": "0.0.0.0",
                    "gateway": "32.68.51.129"
                }
            ]
        },
        {
            "id": "pxe-ipv6",
            "link": "eno4",
            "type": "ipv6",
            "ip_address": "fd00:900:100:138::11"
        },
        {
            "id": "pxe-ipv4",
            "link": "eno4",
            "type": "ipv4",
            "ip_address": "172.30.0.11",
            "netmask": "255.255.255.128"
        },
        {
            "id": "storage-ipv6",
            "link": "bond0.42",
            "type": "ipv6",
            "ip_address": "fd00:900:100:139::15"
        },
        {
            "id": "storage-ipv4",
            "link": "bond0.42",
            "type": "ipv4",
            "ip_address": "172.31.1.15",
            "netmask": "255.255.255.128"
        },
        {
            "id": "ksn-ipv6",
            "link": "bond0.44",
            "type": "ipv6",
            "ip_address": "fd00:900:100:13a::11"
        },
        {
            "id": "ksn-ipv4",
            "link": "bond0.44",
            "type": "ipv4",
            "ip_address": "172.29.0.11",
            "netmask": "255.255.255.128"
        }
    ]
}
***/
func (ml *MachineList) getIp(machine *Machine, networkDataSecret *corev1.Secret, infraServices map[airshipv1.InfraService]airshipv1.InfraConfig) error {
	var secretData interface{}
	// Now I have the Secret
	// Lets find the IP's for all Interfaces defined in Cfg
	for svcName, svcCfg := range infraServices {
		// Did I already find teh IP for these interface
		if machine.Data.IpOnInterface[svcCfg.NodeInterface] == "" {
			foundInterface := false
			foundIp := false
			jsonData := []byte(networkDataSecret.StringData["networkData"])
			json.Unmarshal(jsonData, &secretData)
			networkData := secretData.(map[string]interface{})
			for ndName, ndData := range networkData {
				if ndName == "networks" {
					switch ndData := ndData.(type) {
					case []interface{}:
						for _, ndInfo := range ndData {
							ndInfoSlice := strings.Split(fmt.Sprintf("%v", ndInfo), ":")
							if ndInfoSlice[0] == "id" && ndInfoSlice[0] == svcCfg.NodeInterface {
								foundInterface = true
							}
							if ndInfoSlice[0] == "ip_address" && foundInterface {
								machine.Data.IpOnInterface[svcCfg.NodeInterface] = ndInfoSlice[1]
								foundIp = true
								break
							}
						}
						if foundIp {
							break
						}
					}
				}
				if !foundIp {
					return &ErrorHostIpNotFound{
						HostName:    machine.Bmh.ObjectMeta.Name,
						ServiceName: svcName,
						IPInterface: svcCfg.NodeInterface,
					}
				}
			}
		}
	}
	return nil
}

/*
  ScheduleSet is a simple object to encapsulate data that
  helps our poor man scheduler
*/
type ScheduleSet struct {
	// Defines if this set is actually active
	active bool
	// Holds list of elements in teh Set
	set map[string]bool
	// Holds the label name that identifies the constraint
	labelName string
}

func (ss *ScheduleSet) Active() bool {
	return ss.active
}
func (ss *ScheduleSet) Exists(value string) bool {
	if len(ss.set) > 0 {
		return ss.set[value]
	}
	return false
}

func (ss *ScheduleSet) GetLabels(labels map[string]string, flavorLabel string) (string, bool) {
	if labels == nil {
		return "", false
	}
	cl := strings.Split(flavorLabel, "=")
	if len(cl) > 0 {
		return labels[ss.labelName], labels[cl[0]] == cl[1]
	}
	return labels[ss.labelName], false
}
