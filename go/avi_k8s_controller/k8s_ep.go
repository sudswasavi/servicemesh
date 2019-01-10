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

package main

import (
        "strings"
        "strconv"
        "fmt"
        corev1 "k8s.io/api/core/v1"
        avimodels "github.com/avinetworks/sdk/go/models"
        )

 /* AviCache for storing * Service to E/W Pools and Route/Ingress Pools. 
  * Of the form:
  * map[{namespace: string, name: string}]map[pool_name_prefix:string]bool
  */

type K8sEp struct {
    avi_obj_cache *AviObjCache
    avi_rest_client_pool *AviRestClientPool
    svc_to_pool_cache *AviMultiCache
    svc_to_pg_cache *AviMultiCache
    informers *Informers
}

func NewK8sEp(avi_obj_cache *AviObjCache, avi_rest_client_pool *AviRestClientPool,
              inf *Informers) *K8sEp {
    p := K8sEp{}
    p.svc_to_pool_cache = NewAviMultiCache()
    p.svc_to_pg_cache = NewAviMultiCache()
    p.avi_obj_cache = avi_obj_cache
    p.avi_rest_client_pool = avi_rest_client_pool
    p.informers = inf
    return &p
}

/*
 * This function is called on a CU of Endpoint event handler or on a CU of a 
 * Service, Route or Ingress. 
 * 1) name_prefix will be "" if called directly from ep event handler. In such
 * cases, simply update existing pg/pools affected by the Endpoint. Ignore if
 * pg/pool doesn't exist
 * 2) name_prefix will be non-nil if called from the CU of a Service, Route or
 * Ingress. In such cases, CU the pg/pool
 * TODO: PoolGroup, MicroService, MicroServiceGroup, etc.
 */

func (p *K8sEp) K8sObjCrUpd(shard uint32, ep *corev1.Endpoints,
                name_prefix string, crud_hash_key string) ([]*RestOp, error) {
    /*
     * Endpoints.Subsets is an array with each subset having a list of
     * ready/not-ready addresses and ports. If a endpoint has 2 ports, one ready
     * and the other not ready, there will be 2 subset elements with the same
     * IP as "ready" in one element and "not ready" in the other. Same IP won't
     * be present in both ready and not ready in same element
     *
     * Create a list of all ports with ready endpoints. Lookup Service object
     * in cache and extract targetPort from Service Object. If Service isnt
     * present yet, wait for it to be synced
     */

    port_protocols := make(map[AviPortStrProtocol]bool)
    svc, err := p.informers.ServiceInformer.Lister().Services(ep.Namespace).Get(ep.Name)
    if err != nil {
        AviLog.Warning.Printf("Service for Endpoint Namespace %v Name %v doesn't exist",
                   ep.Namespace, ep.Name)
        return nil, nil
    }
    for _, ss := range ep.Subsets {
        if len(ss.Addresses) > 0 {
            for _, ep_port := range ss.Ports {
                /*
                 * Use the value in TargetPort field of Service. It can be the
                 * port name or value
                 */
                tgt_port := func(svc *corev1.Service, port int32, name string) string {
                        for _, pp := range(svc.Spec.Ports) {
                            if pp.Port == port {
                                return strconv.Itoa(int(port))
                            } else if  pp.Name == name {
                                return name
                            }
                        }
                        AviLog.Warning.Printf("Matching port %v name %v not found in Svc namespace %s name %s",
                                   port, name, svc.Namespace, svc.Name)
                        return ""
                    }(svc, ep_port.Port, ep_port.Name)

                if tgt_port == "" {
                    AviLog.Warning.Printf("Matching port %v name %v not found in Svc",
                                   ep_port.Port, ep_port.Name)
                    return nil, nil
                }

                pp := AviPortStrProtocol{Port: tgt_port,
                        Protocol: strings.ToLower(string(ep_port.Protocol))}
                port_protocols[pp] = true
            }
        }
    }

    var pool_names []string
    var service_metadata ServiceMetadataObj

    process_pool := true
    k := NamespaceName{Namespace: ep.Namespace, Name: ep.Name}
    /*
     * If  name_prefix is nil, this is a Ep CU event from event handler; See
     * if pools/pgs exist already. If so, let's perform U
     */
    if name_prefix == "" {
        var pools_cache interface{}
        var pools *map[string]bool
        var ok bool
        pools_cache, process_pool = p.svc_to_pool_cache.AviMultiCacheGetKey(k)
        pools, ok = pools_cache.(*map[string]bool)
        if process_pool && ok {
            for pool_name := range *pools {
                pool_names = append(pool_names, pool_name)
                var pool_cache interface{}
                pool_cache, ok := p.avi_obj_cache.pool_cache.AviCacheGet(pool_name)
                if !ok {
                    AviLog.Warning.Printf("Pool %s not present in Obj cache but present in Pool cache", pool_name)
                } else {
                    pool_cache_obj, ok := pool_cache.(*AviPoolCache)
                    if ok {
                        service_metadata = pool_cache_obj.ServiceMetadata
                    } else {
                        AviLog.Warning.Printf("Pool %s cache incorrect type", pool_name)
                        service_metadata = ServiceMetadataObj{}
                    }
                }
            }
        }
    } else {
        for pp, _ := range port_protocols {
            pool_name := fmt.Sprintf("%s-pool-%v-%s", name_prefix, pp.Port,
                                   pp.Protocol)
            pool_names = append(pool_names, pool_name)
        }
        service_metadata = ServiceMetadataObj{CrudHashKey: crud_hash_key}
    }

    if !process_pool {
        AviLog.Info.Printf("Endpoint %v is not present in Pool/Pg cache.", k)
        return nil, nil
    }

    var rest_ops []*RestOp

    for _, pool_name := range pool_names {
        pool_meta := K8sAviPoolMeta{Name: pool_name,
            Tenant: ep.Namespace,
            ServiceMetadata: service_metadata,
            CloudConfigCksum: ep.ResourceVersion}
        s := strings.Split(pool_name, "-pool-")
        s1 := strings.Split(s[1], "-")
        port := s1[0]
        port_num, _ := strconv.Atoi(port)
        protocol := s1[1]
        pool_meta.Protocol = protocol
        for _, ss := range ep.Subsets {
            var epp_port int32
            port_match := false
            for _, epp := range ss.Ports {
                if ((int32(port_num) == epp.Port) || (port == epp.Name)) && (protocol == strings.ToLower(string(epp.Protocol))) {
                    port_match = true
                    epp_port = epp.Port
                    break
                }
            }
            if port_match {
                pool_meta.Port = epp_port
                for _, addr := range(ss.Addresses) {
                    var atype string
                    ip := addr.IP
                    if IsV4(addr.IP) {
                        atype = "V4"
                    } else {
                        atype = "V6"
                    }
                    a := avimodels.IPAddr{Type: &atype, Addr: &ip}
                    server := AviPoolMetaServer{Ip: a}
                    if addr.NodeName != nil {
                        server.ServerNode = *addr.NodeName
                    }
                    pool_meta.Servers = append(pool_meta.Servers, server)
                }
            }
        }

        rest_op := AviPoolBuild(&pool_meta)
        rest_ops = append(rest_ops, rest_op)
    }

    if name_prefix == "" {
        p.avi_rest_client_pool.AviRestOperate(p.avi_rest_client_pool.AviClient[shard], rest_ops)
        for _, rest_op := range(rest_ops) {
            if rest_op.Err == nil {
                AviPoolCacheAdd(p.avi_obj_cache.pool_cache, rest_op)
            }
        }
        return nil, nil
    } else {
        return rest_ops, nil
    }
}

func (p *K8sEp) K8sObjDelete(shard uint32, ep *corev1.Endpoints) ([]*RestOp, error) {
    return nil, nil
}
