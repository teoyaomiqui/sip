package vbmh

import (
	"context"
	"math/rand"
	"time"

	"github.com/go-logr/logr"
	metal3 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sipv1 "sipcluster/pkg/api/v1"
)

// Scheduler helps to select and filter BMHs for labeling
type Scheduler struct {
	Logger logr.Logger
	Client client.Client
}

// ListMatchingFlavor lists BaremetalHosts from cluster based on role and flavor
func (s Scheduler) ListMatchingFlavor(flavorSelector string) ([]metal3.BareMetalHost, error){
	logger := s.Logger.WithValues("flavor selector", flavorSelector)

	bmhList := &metal3.BareMetalHostList{}
	ctx, cancel := context.WithTimeout(context.Background(), 30 * time.Second)
	defer cancel()
	
	sel, err  := labels.Parse(flavorSelector)
	if err != nil {
		return nil, err
	}

	req,err := labels.NewRequirement(SipClusterLabel, selection.Exists, []string{})
	if err != nil {
		return nil, err
	}

	flavorListOption :=  client.MatchingLabelsSelector{
		Selector: sel.Add(*req),
	}

	err = s.Client.List(ctx, bmhList, flavorListOption)
	if err != nil {
		logger.Error(err, "Error trying to list matching BMHs by flavor")
		return nil, err
	}
	return bmhList.Items, nil
}

// RandomFromList will randomly filter requested count of nodes based on scheduling algorithm
func (s Scheduler) RandomFromList(input []*metal3.BareMetalHost, schedAlgorithm sipv1.SchedulingOptions, count int) []*metal3.BareMetalHost {
	switch schedAlgorithm {
	case sipv1.RackAntiAffinity, sipv1.ServerAntiAffinity:
		return s.filterPerUnit(input, schedAlgorithm, count)
	case sipv1.SchedulingAlgorithmAny:
		return s.filterAny(input, count)
	default:
		return s.filterAny(input, count)
	}
}

func (s Scheduler) filterPerUnit(input []*metal3.BareMetalHost, schedAlgorithm sipv1.SchedulingOptions, count int) []*metal3.BareMetalHost {
	logger := s.Logger.WithValues("scheduling algorithm", schedAlgorithm, "requested BMH count", count)
	result := []*metal3.BareMetalHost{}
	sorted := s.sortByUnit(schedAlgorithm, input)
	selectedUnits := s.randomKeys(count, sorted)

	rand.Seed(time.Now().UnixNano())

	for _, unitName := range selectedUnits {
		bmhs := sorted[unitName]
		selectedBMH := bmhs[rand.Intn(len(bmhs))]
		result = append(result, selectedBMH)
	}
	logger.Info("Filtering result", "resulting BMH count", len(result))
	return result
}

func (s Scheduler) filterAny(input []*metal3.BareMetalHost, requiredHosts int) []*metal3.BareMetalHost {
	rand.Seed(time.Now().UnixNano())

	rand.Shuffle(len(input), func(i, j int) {
		input[i], input[j] = input[j], input[i]
	})

	if len(input) >= requiredHosts {
		return input[:requiredHosts]
	}
	return input
}

func (s Scheduler) sortByUnit(schedAlgorithm sipv1.SchedulingOptions, input []*metal3.BareMetalHost) map[string][]*metal3.BareMetalHost {
	result := make(map[string][]*metal3.BareMetalHost)
	logger := s.Logger.WithValues("scheduling algorithm", schedAlgorithm)
	logger.Info("got units to sort", "count", len(input))
	var wantedLabel string
	switch schedAlgorithm {
	case sipv1.ServerAntiAffinity:
		wantedLabel = ServerLabel
	case sipv1.RackAntiAffinity:
		wantedLabel = RackLabel
	default:
		return result
	}
	logger = s.Logger.WithValues("wanted label", wantedLabel)
	for _, bmh := range input {
		labels := bmh.GetLabels()
		logger.Info("Sorting BMH",
			"BMH name", bmh.GetName(),
			"BMH namespace", bmh.GetNamespace(),
			"BMH labels", labels)
		labelValue, exist := labels[wantedLabel]
		if exist {
			result[labelValue] = append(result[labelValue], bmh)
		}
	}
	logger.Info("sorted BMHs into units", "unit count", len(result))
	return result
}

func (s Scheduler) randomKeys(count int, input map[string][]*metal3.BareMetalHost) []string {
	keys := []string{}
	for key := range input {
		keys = append(keys, key)
	}
	return s.randomSlice(count, keys)
}

func (s Scheduler) randomSlice(count int, slice []string) []string {
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(slice), func(i, j int) {
		slice[i], slice[j] = slice[j], slice[i]
	})

	if count >= len(slice) {
		return slice
	}
	return slice[:count]
}

