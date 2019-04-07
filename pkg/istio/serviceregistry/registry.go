/*
* [2013] - [2018] Avi Networks Incorporated
* All Rights Reserved.
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*   http://www.apache.org/licenses/LICENSE-2.0
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
 */

package serviceregistry

import (
	"sync"

	"github.com/avinetworks/servicemesh/pkg/utils"
)

// This package is used by kubernetes as well as Istio object updates.
// Keep all implementations generic as much as possible.

type ServiceRegistry struct {
	ServiceMap map[string]*ServiceProperties
	svclock    sync.RWMutex
}

func NewServiceRegistry() *ServiceRegistry {
	registry := &ServiceRegistry{}
	registry.ServiceMap = make(map[string]*ServiceProperties)
	return registry
}

type ServiceProperties struct {
	VsName    []string
	Endpoints []*ServiceEndpoint
	// Sequence is used by the caller to determine if they have the latest copy.
	sequence uint64
	//Include DR.
}

type ServicePropRequest struct {
	SvcName          string
	DeletedVsNames   []string
	AddedVsNames     []string
	DeletedEndpoints []*ServiceEndpoint
	AddedEndpoints   []*ServiceEndpoint
}

func NewServicePropRequest(svcName string) *ServicePropRequest {
	svcPropReq := &ServicePropRequest{}
	svcPropReq.SvcName = svcName
	return svcPropReq
}

func NewServiceProperties() *ServiceProperties {
	svcProp := &ServiceProperties{}
	svcProp.sequence = 0
	return svcProp

}

type IstioVirtualServiceList struct {
	vsName []string
}

func (s *ServiceRegistry) readOrCreateServiceEntry(svcName string) *ServiceProperties {
	// This method is called to return a ServiceProperty. If the ServiceProperty exists, it returns back
	// the prsent serviceproperty, if it's not present, it creates a new service property and sends.
	s.svclock.RLock()
	defer s.svclock.RUnlock()
	val, ok := s.ServiceMap[svcName]
	if !ok {
		// Create a fresh service property.
		svcProp := NewServiceProperties()
		s.ServiceMap[svcName] = svcProp
		utils.AviLog.Trace.Println("Creating empty service property for : ", svcName)
		return svcProp
	} else {
		return val
	}
}

func (s *ServiceRegistry) GetService(svcName string) *ServiceProperties {
	// This method is called to return a ServiceProperty.
	s.svclock.RLock()
	defer s.svclock.RUnlock()
	val, ok := s.ServiceMap[svcName]
	if !ok {
		utils.AviLog.Info.Println("Service not found in Registry: ", svcName)
		return nil
	} else {
		return val
	}
}

func (s *ServiceRegistry) DeleteServiceEntry(svcName string) bool {
	// This method is only called upon a kubernetes service creation or a ServiceEntry CRD creation.
	s.svclock.RLock()
	defer s.svclock.RUnlock()
	delete(s.ServiceMap, svcName)
	return true
}

func (s *ServiceRegistry) MergeServiceProperties(svcPropReq *ServicePropRequest) (*ServiceProperties, bool) {
	s.svclock.RLock()
	defer s.svclock.RUnlock()
	storedSvcProp := s.readOrCreateServiceEntry(svcPropReq.SvcName)
	storedSvcProp.VsName = append(svcPropReq.AddedVsNames, storedSvcProp.VsName...)
	storedSvcProp.Endpoints = append(svcPropReq.AddedEndpoints, storedSvcProp.Endpoints...)
	//Additional check required to ensure all the requested endpoints previously existed in the storedSvcProp
	storedSvcProp.VsName = Difference(storedSvcProp.VsName, svcPropReq.AddedVsNames)
	//storedSvcProp.Endpoints = Difference(storedSvcProp.Endpoints, svcPropReq.deletedEndpoints)
	return storedSvcProp, true
}

//Return elements that are in 'stored' but not in 'requested'.

func Difference(stored, requested []string) []string {
	requestedMap := map[string]bool{}
	for _, elem := range requested {
		requestedMap[elem] = true
	}
	var diff []string
	for _, elem := range stored {
		if _, ok := requestedMap[elem]; !ok {
			diff = append(diff, elem)
		}
	}
	return diff
}

func (s *ServiceProperties) FindEndpointWithLabels(label string) []*ServiceEndpoint {
	// For a given ServiceProperty - this method should return all the endpoints that match a given endpoint label
	var epsMatched []*ServiceEndpoint
	for _, ep := range s.Endpoints {
		if ep.Labels.SearchLabel(label) {
			epsMatched = append(epsMatched, ep)
		}
	}
	return epsMatched
}

func (s *ServiceProperties) UpdateEndpointsWithLabels(label string, epPort string) []*ServiceEndpoint {
	// For a given endpoint, update the labels
	var epsMatched []*ServiceEndpoint
	for _, ep := range s.Endpoints {
		if ep.Labels.SearchLabel(label) {
			epsMatched = append(epsMatched, ep)
		}
	}
	return epsMatched
}

type Labels map[string]string

type ServiceEndpoint struct {
	// Labels points to the workload or deployment labels.
	// TODO(sudswas): Consider a check for unix socket domains if we support it in the future.
	// (192.168.1.2:98, lbweight: 20, labels(foo=bar))
	// (192.168.2.3:98, lbweight: 20, labels(git=2de4))
	Labels
	Servers  []utils.AviPoolMetaServer
	Port     int32
	Protocol string
	// The load balancing weight associated with this endpoint.
	LbWeight uint32
}

func (e Labels) AddLabel(key string, value string) {
	_, ok := e[key]
	if !ok {
		// Take care of overwriting labels?
		e[key] = value
	}
}

func (e Labels) RemoveLabel(key string) {
	// Check if the key exists
	_, ok := e[key]
	if ok {
		delete(e, key)
	}
}

func (e Labels) SearchLabel(label string) bool {
	_, ok := e[label]
	if ok {
		return true
	} else {
		return false
	}
}
