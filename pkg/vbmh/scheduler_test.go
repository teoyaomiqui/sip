package vbmh_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	metal3 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	v1 "sipcluster/pkg/api/v1"
	"sipcluster/pkg/vbmh"
	"sipcluster/testutil"
)

var _ = Describe("Scheduler", func() {
	scheduler := vbmh.Scheduler{
		Logger: zap.Logger(true),
	}
	It("Schedules single BMH", func() {
		By("By using single given server with correct rack label")
		bmh, _ := testutil.CreateBMH(0, 1, "default", "worker", true)
		Expect(scheduler.RandomFromList([]*metal3.BareMetalHost{bmh}, v1.RackAntiAffinity, 1)).To(HaveLen(1))
	})
	It("Schedules 6 BMHs out 10", func() {
		By("Picking unique hosts in different racks")
		bmhs := []*metal3.BareMetalHost{}
		for i := 0; i <= 10; i++ {
			bmh, _ := testutil.CreateBMH(i, i, "default", "worker", true)
			bmhs = append(bmhs, bmh)
		}
		result := scheduler.RandomFromList(bmhs, v1.RackAntiAffinity, 6)
		Expect(result).To(HaveLen(6))
		allUnique(result)
	})
	It("Schedules 10 BMHs out 10", func() {
		By("Picking unique hosts in different racks")
		bmhs := []*metal3.BareMetalHost{}
		for i := 0; i <= 9; i++ {
			bmh, _ := testutil.CreateBMH(i, i, "default", "worker", true)
			bmhs = append(bmhs, bmh)
		}
		result := scheduler.RandomFromList(bmhs, v1.RackAntiAffinity, 10)
		Expect(result).To(HaveLen(10))
		allUnique(result)
	})
	It("Schedules 10 BMHs out 10", func() {
		By("Picking unique hosts in different servers")
		bmhs := []*metal3.BareMetalHost{}
		for i := 0; i <= 9; i++ {
			bmh, _ := testutil.CreateBMH(i, i, "default", "worker", true)
			bmhs = append(bmhs, bmh)
		}
		result := scheduler.RandomFromList(bmhs, v1.ServerAntiAffinity, 10)
		Expect(result).To(HaveLen(10))
		allUnique(result)
	})
	It("Schedule 5 BMH", func() {
		By("Picking 5 vBMH in a single rack")
		bmhs := []*metal3.BareMetalHost{}
		for i := 0; i <= 4; i++ {
			bmh, _ := testutil.CreateBMH(i, 1, "default", "worker", true)
			bmhs = append(bmhs, bmh)
		}
		result := scheduler.RandomFromList(bmhs, v1.ServerAntiAffinity, 5)
		Expect(result).To(HaveLen(5))
		allUnique(result)
	})
})

func allUnique(input []*metal3.BareMetalHost) {
	namespacedNames := make(map[string]bool)
	for _, bmh := range input {
		namespacedName := bmh.GetNamespace() + "/" + bmh.GetName()
		if _, exists := namespacedNames[namespacedName]; !exists {
			namespacedNames[namespacedName] = true
		}
	}
	ExpectWithOffset(1, namespacedNames).To(HaveLen(len(input)))

}
