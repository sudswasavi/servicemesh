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

package objects

import (
	"fmt"
	"strings"

	registry "github.com/avinetworks/servicemesh/pkg/istio/serviceregistry"
	"github.com/avinetworks/servicemesh/pkg/utils"
	networking "istio.io/api/networking/v1alpha3"
)

const (
	IstioMeshGateway = "mesh"

	IstioSystemNamespace = "istio-system"
)

func AviValidateGatewaysAndPopulate(vs *IstioObject, vsGwMap *GatewayToVsMap) []string {
	vsObj, _ := vs.Spec.(*networking.VirtualService)
	namespace := vs.ConfigMeta.Namespace
	var gateways []string
	if len(vsObj.Gateways) == 0 {
		utils.AviLog.Warning.Println("Avi does not support virtual services for internal traffic")
	}
	for _, gateway := range vsObj.Gateways {
		if gateway != IstioMeshGateway {
			gatewayNamespaced := namespace + ":" + gateway
			vsNamespaced := namespace + ":" + vs.ConfigMeta.Name
			vsGwMap.AddVsToGateway(vsNamespaced, gatewayNamespaced)
			gateways = append(gateways, gatewayNamespaced)
		}
	}
	return gateways
}

func GetGatewayForVS(vs *IstioObject) []string {
	vsObj, _ := vs.Spec.(*networking.VirtualService)
	namespace := vs.ConfigMeta.Namespace
	var gateways []string
	if len(vsObj.Gateways) == 0 {
		utils.AviLog.Warning.Println("Avi does not support virtual services for internal traffic")
	}
	for _, gateway := range vsObj.Gateways {
		if gateway != IstioMeshGateway {
			gatewayNamespaced := namespace + ":" + gateway
			gateways = append(gateways, gatewayNamespaced)
		}
	}
	return gateways
}

func (o *IstioObjectOpsController) ProcessIstioVirtualService(vsKey string, vsGwMap *GatewayToVsMap) (errs error) {
	// Split the VS key and the Gateway
	vs_gw := strings.SplitN(vsKey, "/", 2)
	vs := NewVsMap.GetObj(vs_gw[0])
	vsObj, _ := vs.Spec.(*networking.VirtualService)
	vsName := vs.ConfigMeta.Name
	namespace := vs.ConfigMeta.Namespace
	//crud_hash_key := namespace + ":" + vsName
	//svc_mdata := utils.ServiceMetadataObj{CrudHashKey: crud_hash_key}
	gateways := AviValidateGatewaysAndPopulate(vs, vsGwMap)
	for _, gateway := range gateways {
		// Let's check if the Gateway for this VS is already created. We will assume a VS to be bound by a single Gateway for now.
		// But we should be able to scale that in the future.
		gatewayNamespaced := namespace + ":" + gateway
		//gwCreated := VsGwMap.IsGatewayCreated(gatewayNamespaced)
		vsNamespaced := namespace + ":" + vsName
		VsGwMap.AddVsToGateway(vsNamespaced, gatewayNamespaced)
		if true {
			// If the gateway is created, it's highly likely that AVI has already processed a VS for it. So let's fetch the same.
			vs_cache_key := utils.NamespaceName{Namespace: namespace, Name: gateway}
			vs_cache, _ := o.avi_obj_cache.VsCache.AviCacheGet(vs_cache_key)
			utils.AviLog.Trace.Println("Found VS cache: ", vs_cache)
		}
	}
	//pgNames, poolNames := GetPoolAndPoolGroup(vs)
	//fmt.Println(pgNames)
	//fmt.Println(poolNames)
	for _, tlsRoute := range vsObj.Tls {
		utils.AviLog.Info.Println("We don't handle TLS yet", tlsRoute)
	}
	for _, tcpRoute := range vsObj.Tcp {
		utils.AviLog.Info.Println("We don't handle TCP yet", tcpRoute)
	}
	return nil
}

func GetHTTPPoolAndPoolGroup(vs *IstioObject) ([]string, []string, map[string]*registry.ServiceProperties) {
	vsObj, _ := vs.Spec.(*networking.VirtualService)
	vsName := vs.ConfigMeta.Name
	var pgNames []string
	var poolNames []string
	poolProperties := make(map[string]*registry.ServiceProperties)
	for _, httpRoute := range vsObj.Http {
		for _, match := range httpRoute.Match {
			prefix_match := match.Uri
			utils.AviLog.Info.Println("To be supported", prefix_match)
		}
		for _, destination := range httpRoute.Route {
			var subset string
			var pgName string
			var poolName string
			var portName string
			var portNumber int
			svcName := destination.Destination.Host
			// Initialize a request for the Service Registry
			registryRequest := registry.NewServicePropRequest(svcName)
			registryRequest.AddedVsNames = []string{vsName}
			serviceProperties, _ := SvcRegistry.MergeServiceProperties(registryRequest)

			if destination.Destination.Subset != "" {
				utils.AviLog.Info.Println("Will not handle subsets right now")
				subset = destination.Destination.Subset
			}
			if destination.Destination.Port != nil {
				portName = destination.Destination.Port.GetName()
				portNumber = int(destination.Destination.Port.GetNumber())
			}
			if portName != "" {
				pgName = fmt.Sprintf("poolgroup-%s-%s-%s-%s", vsName, "http",
					svcName, portName)
				poolName = fmt.Sprintf("pool-%s-%s-%s-%s", vsName, "http",
					svcName, portName)
				poolProperties[poolName] = serviceProperties
				pgNames = append(pgNames, pgName)
				poolNames = append(poolNames, poolName)
			} else if subset != "" {
				pgName = fmt.Sprintf("poolgroup-%s-%s-%s-%s", vsName, "http",
					svcName, subset)
				poolName = fmt.Sprintf("pool-%s-%s-%s-%s", vsName, "http",
					svcName, subset)
				poolProperties[poolName] = serviceProperties
				pgNames = append(pgNames, pgName)
				poolNames = append(poolNames, poolName)
			} else if portNumber != 0 {
				pgName = fmt.Sprintf("poolgroup-%s-%s-%s-%v", vsName, "http",
					svcName, portNumber)
				poolName = fmt.Sprintf("poolgroup-%s-%s-%s-%v", vsName, "http",
					svcName, portNumber)
				poolProperties[poolName] = serviceProperties
				pgNames = append(pgNames, pgName)
				poolNames = append(poolNames, poolName)
			} else {
				pgName = fmt.Sprintf("poolgroup-%s-%s-%s", vsName, "http",
					svcName)
				poolName = fmt.Sprintf("pool-%s-%s-%s", vsName, "http",
					svcName)
				poolProperties[poolName] = serviceProperties
				pgNames = append(pgNames, pgName)
				poolNames = append(poolNames, poolName)
			}

		}
	}
	return pgNames, poolNames, poolProperties
}
