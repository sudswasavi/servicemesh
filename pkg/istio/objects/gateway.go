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

	avimodels "github.com/avinetworks/sdk/go/models"

	"github.com/avinetworks/servicemesh/aviobjects"
	"github.com/avinetworks/servicemesh/pkg/utils"
	"github.com/golang/protobuf/proto"
	networking "istio.io/api/networking/v1alpha3"
)

// All gateway validations go here - Pilot already performs a bunch of validations. So we have to decide to

func ValidateGateway(name string, namespace string, gw proto.Message) (errs error) {
	return nil
}

// All gateway processing goes here

func (o *IstioObjectOpsController) ProcessGatewayAdd(gw *IstioObject, vsGwMap *GatewayToVsMap) (errs error) {
	// Let's rock - Call AVI APIs to create the VS
	var rest_ops []*utils.RestOp
	gwObj, _ := gw.Spec.(*networking.Gateway)
	gatewayName := gw.ConfigMeta.Name
	namespace := gw.ConfigMeta.Namespace
	crud_hash_key := namespace + ":" + gatewayName
	vs_cache_key := utils.NamespaceName{Namespace: namespace, Name: gatewayName}
	vs_cache, found := o.avi_obj_cache.VsCache.AviCacheGet(vs_cache_key)
	if found {
		vs_cache_obj, ok := vs_cache.(*utils.AviVsCache)
		if ok {
			if vs_cache_obj.CloudConfigCksum == gw.ConfigMeta.ResourceVersion {
				utils.AviLog.Info.Printf(`Gateway namespace %s name %s has same 
                    resourceversion %s`, gw.ConfigMeta.Namespace, gw.ConfigMeta.Name,
					gw.ConfigMeta.ResourceVersion)
				vsGwMap.PopulateGWCreated(gatewayName)
				return nil
			} else {
				utils.AviLog.Info.Printf(`Gateway namespace %s name %s resourceversion 
                        %s different from cksum %s`, gw.ConfigMeta.Namespace, gw.ConfigMeta.Name,
					gw.ConfigMeta.ResourceVersion, vs_cache_obj.CloudConfigCksum)
			}
		} else {
			utils.AviLog.Info.Printf("Gateway namespace %s name %s not found in cache",
				gw.ConfigMeta.Namespace, gw.ConfigMeta.Name)
		}
	}
	// Check if this gateway has any mapped VSes.
	gatewayNamespaced := namespace + ":" + gatewayName
	vsNames, vsFound := VsGwMap.GetVsFromGateway(gatewayNamespaced)
	svc_mdata := utils.ServiceMetadataObj{CrudHashKey: crud_hash_key}
	avi_vs_meta := utils.K8sAviVsMeta{Name: gatewayName, Tenant: namespace,
		CloudConfigCksum: gw.ConfigMeta.ResourceVersion, ServiceMetadata: svc_mdata,
		EastWest: false, FQDN: gatewayName + ".avi.internal"}
	protocolPortMap := make(map[string][]int32)
	avi_vs_meta.PoolGroupMap = make(map[utils.AviPortProtocol]string)

	for _, s := range gwObj.Servers {
		_, ok := protocolPortMap[s.Port.Protocol]
		if ok {
			// Append the port to protocol list
			protocolPortMap[s.Port.Protocol] = append(protocolPortMap[s.Port.Protocol], int32(s.Port.Number))
		} else {
			protocolPortMap[s.Port.Protocol] = []int32{int32(s.Port.Number)}
		}
		if !vsFound {
			pp := utils.AviPortProtocol{Port: int32(s.Port.Number), Protocol: s.Port.Protocol}
			avi_vs_meta.PortProto = append(avi_vs_meta.PortProto, pp)
		}
	}
	if vsFound {
		// We found VSes for this Gateway. Let's create the pools/poolgroups.
		for _, vs := range vsNames {
			// Look up the VS object and then fetch the pgs
			vsObj := NewVsMap.GetObj(vs)
			if vsObj != nil {
				// Are there any HTTP ports configured in the gateway?
				for protocol, port_numbers := range protocolPortMap {
					if protocol == "HTTP" {
						// Let's process the HTTP stuff here.
						pgNames, poolNames, servicePropList := GetHTTPPoolAndPoolGroup(vsObj)
						crud_hash_key := gatewayNamespaced
						pg_service_metadata := utils.ServiceMetadataObj{CrudHashKey: crud_hash_key}
						for _, pool_name := range poolNames {
							// Check if resourceVersion is same as cksum from cache. If so, skip upd
							pool_key := utils.NamespaceName{Namespace: namespace, Name: pool_name}
							pool_cache, ok := o.avi_obj_cache.PoolCache.AviCacheGet(pool_key)
							if !ok {
								utils.AviLog.Warning.Printf("Namespace %s Pool %s not present in Pool cache",
									namespace, pool_name)
							} else {
								pool_cache_obj, ok := pool_cache.(*utils.AviPoolCache)
								if ok {
									if vsObj.ConfigMeta.ResourceVersion == pool_cache_obj.CloudConfigCksum {
										utils.AviLog.Info.Printf("Pool namespace %s name %s has same cksum %s",
											namespace, pool_name, vsObj.ConfigMeta.ResourceVersion)
										continue
									} else {
										utils.AviLog.Info.Printf(`Pool namespace %s name %s has diff 
										cksum %s resourceVersion %s`, namespace, pool_name,
											pool_cache_obj.CloudConfigCksum, vsObj.ConfigMeta.ResourceVersion)
									}
								} else {
									utils.AviLog.Warning.Printf("Pool %s cache incorrect type", pool_name)
								}
							}
							pool_meta := utils.K8sAviPoolMeta{Name: pool_name,
								Tenant:           namespace,
								ServiceMetadata:  utils.ServiceMetadataObj{CrudHashKey: crud_hash_key},
								CloudConfigCksum: vsObj.ConfigMeta.ResourceVersion}
							for _, serviceProperty := range servicePropList {
								for _, serviceEp := range serviceProperty.Endpoints {
									pool_meta.Servers = serviceEp.Servers
									pool_meta.Port = serviceEp.Port
								}
							}
							//Hardcoding
							pool_meta.Protocol = "http"
							eastWest := false
							pool_meta.EastWest = &eastWest
							rest_op := aviobjects.AviPoolBuild(&pool_meta)
							rest_ops = append(rest_ops, rest_op)
						}
						for _, pg_name := range pgNames {
							// Check if resourceVersion is same as cksum from cache. If so, skip upd
							pg_key := utils.NamespaceName{Namespace: namespace, Name: pg_name}
							pg_cache, ok := o.avi_obj_cache.PgCache.AviCacheGet(pg_key)
							if !ok {
								utils.AviLog.Info.Printf("Namespace %s PG %s not present in PG cache",
									namespace, pg_name)
							} else {
								pg_cache_obj, ok := pg_cache.(*utils.AviPGCache)
								if ok {
									if vsObj.ConfigMeta.ResourceVersion == pg_cache_obj.CloudConfigCksum {
										utils.AviLog.Info.Printf("PG namespace %s name %s has same cksum %s",
											namespace, pg_name, vsObj.ConfigMeta.ResourceVersion)
										continue
									} else {
										utils.AviLog.Info.Printf(`PG namespace %s name %s has diff
										cksum %s resourceVersion %s`, namespace, pg_name,
											pg_cache_obj.CloudConfigCksum, vsObj.ConfigMeta.ResourceVersion)
									}
								} else {
									utils.AviLog.Warning.Printf("PG %s cache incorrect type", pg_name)
								}
							}
							for _, port := range port_numbers {
								pp := utils.AviPortProtocol{Port: int32(port), Protocol: protocol}
								avi_vs_meta.PortProto = append(avi_vs_meta.PortProto, pp)
								avi_vs_meta.PoolGroupMap[pp] = pg_name
							}
							// TODO (sudswas): Logic may change if there are many pools inside a poolgroup
							s := strings.Replace(pg_name, "poolgroup", "pool", 1)
							pg_meta := utils.K8sAviPoolGroupMeta{Name: pg_name,
								Tenant:           namespace,
								ServiceMetadata:  pg_service_metadata,
								CloudConfigCksum: vsObj.ConfigMeta.ResourceVersion}
							pool_ref := fmt.Sprintf("/api/pool?name=%s", s)
							// TODO (sudswas): Add priority label, Ratio
							pg_meta.Members = append(pg_meta.Members, &avimodels.PoolGroupMember{PoolRef: &pool_ref})
							rest_op := aviobjects.AviPoolGroupBuild(&pg_meta)
							rest_ops = append(rest_ops, rest_op)
							avi_vs_meta.DefaultPoolGroup = pg_name
						}
					}
				}
			}
		}
	}

	// Needs more check.
	is_http := false
	if len(avi_vs_meta.PoolGroupMap) > 1 {
		is_http = true
	}

	if is_http {
		avi_vs_meta.ApplicationProfile = "System-HTTP"
	} else {
		avi_vs_meta.ApplicationProfile = "System-L4-Application"
	}
	avi_vs_meta.NetworkProfile = "System-TCP-Proxy"
	rops := aviobjects.AviVsBuild(&avi_vs_meta)
	rest_ops = append(rest_ops, rops...)
	// Shard on Gateway name
	bkt := Bkt(crud_hash_key, o.num_workers)
	aviClient := o.avi_rest_client_pool.AviClient[bkt]
	err := o.avi_rest_client_pool.AviRestOperate(aviClient, rest_ops)
	if err != nil {
		utils.AviLog.Warning.Printf("Error %v with rest_ops", err)
		// Iterate over rest_ops in reverse and delete created objs
		for i := len(rest_ops) - 1; i >= 0; i-- {
			if rest_ops[i].Err == nil {
				resp_arr, ok := rest_ops[i].Response.([]interface{})
				if !ok {
					utils.AviLog.Warning.Printf("Invalid resp type for rest_op %v", rest_ops[i])
					continue
				}
				resp, ok := resp_arr[0].(map[string]interface{})
				if ok {
					uuid, ok := resp["uuid"].(string)
					if !ok {
						utils.AviLog.Warning.Printf("Invalid resp type for uuid %v",
							resp)
						continue
					}
					url := utils.AviModelToUrl(rest_ops[i].Model) + "/" + uuid
					err := aviClient.AviSession.Delete(url)
					if err != nil {
						utils.AviLog.Warning.Printf("Error %v deleting url %v", err, url)
					} else {
						utils.AviLog.Info.Printf("Success deleting url %v", url)
					}
				} else {
					utils.AviLog.Warning.Printf("Invalid resp for rest_op %v", rest_ops[i])
				}
			}
		}
	} else {
		// Add to local obj caches
		for _, rest_op := range rest_ops {
			if rest_op.Err == nil {
				if rest_op.Model == "Pool" {
					aviobjects.AviPoolCacheAdd(o.avi_obj_cache.PoolCache, rest_op)
					aviobjects.AviSvcToPoolCacheAdd(o.avi_obj_cache.SvcToPoolCache, rest_op,
						"service", vs_cache_key)
				} else if rest_op.Model == "VirtualService" {
					vsGwMap.PopulateGWCreated(gatewayName)
					aviobjects.AviVsCacheAdd(o.avi_obj_cache.VsCache, rest_op)
				} else if rest_op.Model == "PoolGroup" {
					aviobjects.AviPGCacheAdd(o.avi_obj_cache.PgCache, rest_op)
				}
			}
		}
	}
	return err
}
