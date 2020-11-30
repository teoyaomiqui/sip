package vbmh

import (
	"math/rand"
	"time"

	metal3 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"

	sipv1 "sipcluster/pkg/api/v1"
)

func Schedule(input []metal3.BareMetalHost, schedAlgorithm sipv1.SchedulingOptions, requiredHosts int) []metal3.BareMetalHost {

	switch schedAlgorithm {
	case sipv1.RackAntiAffinity:
		return filterPerUnit(input, schedAlgorithm, requiredHosts)
	case sipv1.SchedulingAlgorithmAny:
		return filterAny(input, requiredHosts)
	default:
		return filterAny(input, requiredHosts)
	}
}

func filterPerUnit(input []metal3.BareMetalHost, schedAlgorithm sipv1.SchedulingOptions, count int) []metal3.BareMetalHost {
	result := []metal3.BareMetalHost{}

	sorted := sortByUnit(schedAlgorithm, input)

	selectedUnits := randomKeys(count, sorted)

	rand.Seed(time.Now().UnixNano())

	for _, unitName := range selectedUnits {
		bmhs := sorted[unitName]
		selectedBMH := bmhs[rand.Intn(len(bmhs))]
		result = append(result, selectedBMH)
	}
	return result
}

func filterAny(input []metal3.BareMetalHost, requiredHosts int) []metal3.BareMetalHost {
	rand.Seed(time.Now().UnixNano())

	rand.Shuffle(len(input), func(i, j int) {
		input[i], input[j] = input[j], input[i]
	})

	if len(input) >= requiredHosts {
		return input[:requiredHosts]
	}
	return input
}

func sortByUnit(schedAlgorithm sipv1.SchedulingOptions, input []metal3.BareMetalHost) map[string][]metal3.BareMetalHost {
	result := make(map[string][]metal3.BareMetalHost)
	var wantedLabel string
	switch schedAlgorithm {
	case sipv1.ServerAntiAffinity:
		wantedLabel = ServerLabel
	case sipv1.RackAntiAffinity:
		wantedLabel = RackLabel
	default:
		return result
	}
	for _, bmh := range input {
		labels := bmh.GetLabels()
		labelValue, exist := labels[wantedLabel]
		if exist {
			unit := result[labelValue]
			unit = append(unit, bmh)
		}
	}
	return result
}

func randomKeys(count int, input map[string][]metal3.BareMetalHost) []string {
	keys := []string{}
	for key := range input {
		keys = append(keys, key)
	}
	return randomSlice(count, keys)
}

func randomSlice(count int, slice []string) []string {
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(slice), func(i, j int) {
		slice[i],slice[j] = slice[j], slice[i]
	})

	if count >= len(slice) {
		return slice
	}
	return slice[:count]
}
