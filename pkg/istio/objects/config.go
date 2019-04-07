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
	"time"

	"github.com/avinetworks/servicemesh/pkg/istio/serviceregistry"
	"github.com/avinetworks/servicemesh/pkg/utils"
	"github.com/golang/protobuf/proto"
)

const (
	GATEWAY             = "gateway"
	ISTIOVIRTUALSERVICE = "virtual-service"
)

var OldGwMap *IstioObjectMap
var OldVsMap *IstioObjectMap
var NewGwMap *IstioObjectMap
var NewVsMap *IstioObjectMap
var OpsCtrl *IstioObjectOpsController
var VsGwMap *GatewayToVsMap
var SvcRegistry *serviceregistry.ServiceRegistry

func InitObjects(avi_obj_cache *utils.AviObjCache, avi_rest_client_pool *utils.AviRestClientPool) *IstioObjectOpsController {
	// Initialize the recognized type maps
	OldGwMap = NewIstioObjectMap()
	OldVsMap = NewIstioObjectMap()
	OpsCtrl = NewIstioObjectOpsController(avi_obj_cache, avi_rest_client_pool)
	VsGwMap = NewGatewayToVsMap()
	SvcRegistry = serviceregistry.NewServiceRegistry()
	return OpsCtrl

}

type ConfigMeta struct {
	// Type is a short configuration name that matches the content message type
	// (e.g. "route-rule")
	Type string `json:"type,omitempty"`

	// Group is the API group of the config.
	Group string `json:"group,omitempty"`

	// Version is the API version of the Config.
	Version string `json:"version,omitempty"`

	// Name is a unique immutable identifier in a namespace
	Name string `json:"name,omitempty"`

	// Namespace defines the space for names (optional for some types),
	// applications may choose to use namespaces for a variety of purposes
	// (security domains, fault domains, organizational domains)
	Namespace string `json:"namespace,omitempty"`

	// Domain defines the suffix of the fully qualified name past the namespace.
	// Domain is not a part of the unique key unlike name and namespace.
	Domain string `json:"domain,omitempty"`

	// Map of string keys and values that can be used to organize and categorize
	// (scope and select) objects.
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations is an unstructured key value map stored with a resource that may be
	// set by external tools to store and retrieve arbitrary metadata. They are not
	// queryable and should be preserved when modifying objects.
	Annotations map[string]string `json:"annotations,omitempty"`

	ResourceVersion string `json:"resourceVersion,omitempty"`
	// CreationTimestamp records the creation time
	CreationTimestamp time.Time `json:"creationTimestamp,omitempty"`
}

func (c ConfigMeta) QueueByTypes(spec proto.Message) {
	switch c.Type {
	case GATEWAY:
		// Hydrate the Gateway Structs
		gw := NewIstioObject(c, spec)
		key := c.Namespace + ":" + c.Name
		NewGwMap.AddObj(key, gw)

	case ISTIOVIRTUALSERVICE:
		// Hydrate the VS Structs
		vs := NewIstioObject(c, spec)
		key := c.Namespace + ":" + c.Name
		utils.AviLog.Trace.Println("Adding VS ", key)
		NewVsMap.AddObj(key, vs)
	}
}

func InitializeObjs(objType string) {
	// Initialize the objects
	switch objType {
	case GATEWAY:
		NewGwMap = NewIstioObjectMap()
	case ISTIOVIRTUALSERVICE:
		NewVsMap = NewIstioObjectMap()
	}
}

func CalculateUpdates(objType string) {
	/* This method compares the newMap with the old Map and finds out the items to delete/update/create.
	Eventually newMap replaces the old Map. We look at the old map and compare every key with the new map,
	if a key is absent in the new map - we assume it's a candidate for delete.
	If the key is present and the resourceVersion is different, we assume it's an update.
	If the key is present and the resourceVersion is same, we no-op.
	If the key is absent in the old map but present in the newMap - we assume that it's an add operation. */
	switch objType {
	case GATEWAY:
		for newKey, newValue := range NewGwMap.ObjMap {
			ok, val := OldGwMap.GetObjByNameNamespace(newKey)
			if !ok {
				key := GATEWAY + "/" + newKey
				// Key is not found in the old map - it's an add
				OpsCtrl.AddOps(key, ADD)
			} else {
				// Compare if the resourceVersions are same
				if val.ConfigMeta.ResourceVersion != newValue.ConfigMeta.ResourceVersion {
					// It's an update
					key := GATEWAY + "/" + newKey
					OpsCtrl.AddOps(key, UPDATE)
				}
			}
		}
		for oldKey, _ := range OldGwMap.ObjMap {
			ok, _ := NewGwMap.GetObjByNameNamespace(oldKey)
			if !ok {
				key := GATEWAY + "/" + oldKey
				OpsCtrl.AddOps(key, DELETE)
			}
		}
		// Now let's swap the old with the new
		OldGwMap.ObjMap = NewGwMap.ObjMap
	case ISTIOVIRTUALSERVICE:
		for newKey, newValue := range NewVsMap.ObjMap {
			ok, val := OldVsMap.GetObjByNameNamespace(newKey)
			if !ok {
				key := ISTIOVIRTUALSERVICE + "/" + newKey
				// Key is not found in the old map - it's an add
				OpsCtrl.AddOps(key, ADD)
			} else {
				// Compare if the resourceVersions are same
				if val.ConfigMeta.ResourceVersion != newValue.ConfigMeta.ResourceVersion {
					// It's an update
					key := ISTIOVIRTUALSERVICE + "/" + newKey
					OpsCtrl.AddOps(key, UPDATE)
				}
			}
		}
		for oldKey, _ := range OldVsMap.ObjMap {
			ok, _ := NewVsMap.GetObjByNameNamespace(oldKey)
			if !ok {
				key := ISTIOVIRTUALSERVICE + "/" + oldKey
				OpsCtrl.AddOps(key, DELETE)
			}
		}
		// Now let's swap the old with the new
		OldVsMap.ObjMap = NewVsMap.ObjMap
	}
}
