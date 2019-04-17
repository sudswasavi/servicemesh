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

package mcp

import (
	"fmt"
	"strings"
	"sync"
	"time"

	istio_objs "github.com/avinetworks/servicemesh/pkg/istio/objects"
	"github.com/avinetworks/servicemesh/pkg/utils"
	"github.com/gogo/protobuf/types"
	"istio.io/istio/pkg/mcp/sink"
)

type Controller struct {
	syncedMu                sync.Mutex
	synced                  map[string]bool
	descriptorsByCollection map[string]ProtoSchema
	avi_obj_cache           *utils.AviObjCache
	avi_rest_client_pool    *utils.AviRestClientPool
	OpsCtrl                 *istio_objs.IstioObjectOpsController
}

func NewController(avi_obj_cache *utils.AviObjCache, avi_rest_client_pool *utils.AviRestClientPool, stopCh <-chan struct{}) *Controller {
	synced := make(map[string]bool)
	descriptorsByMessageName := make(map[string]ProtoSchema, len(IstioConfigTypes))
	for _, descriptor := range IstioConfigTypes {
		// don't register duplicate descriptors for the same collection
		if _, ok := descriptorsByMessageName[descriptor.Collection]; !ok {
			descriptorsByMessageName[descriptor.Collection] = descriptor
			synced[descriptor.Collection] = false
		}
	}
	opsCtrl := istio_objs.InitObjects(avi_obj_cache, avi_rest_client_pool)
	return &Controller{
		synced:                  synced,
		descriptorsByCollection: descriptorsByMessageName,
		avi_obj_cache:           avi_obj_cache,
		avi_rest_client_pool:    avi_rest_client_pool,
		OpsCtrl:                 opsCtrl,
	}
}

// HasSynced is used to tell the MCP server that the first set of items that were
// supposed to be sent to this client for the registered types has been received.
func (c *Controller) HasSynced() bool {
	var notReady []string

	c.syncedMu.Lock()
	for messageName, synced := range c.synced {
		if !synced {
			notReady = append(notReady, messageName)
		}
	}
	c.syncedMu.Unlock()

	if len(notReady) > 0 {
		//log.Infof("Configuration not synced: first push for %v not received", notReady)
		return false
	}
	return true
}

// ConfigDescriptor returns all the ConfigDescriptors that this
// controller is responsible for
func (c *Controller) ConfigDescriptor() ConfigDescriptor {
	return IstioConfigTypes
}

// Apply receives changes from MCP server and creates the
// corresponding config
func (c *Controller) Apply(change *sink.Change) error {
	descriptor, ok := c.descriptorsByCollection[change.Collection]
	if !ok {
		return fmt.Errorf("apply type not supported %s", change.Collection)
	}

	schema, valid := c.ConfigDescriptor().GetByType(descriptor.Type)
	if !valid {
		return fmt.Errorf("descriptor type not supported %s", change.Collection)
	}
	c.syncedMu.Lock()
	c.synced[change.Collection] = true
	c.syncedMu.Unlock()
	istio_objs.InitializeObjs(descriptor.Type)
	createTime := time.Now()
	for _, obj := range change.Objects {
		namespace, name := extractNameNamespace(obj.Metadata.Name)
		utils.AviLog.Info.Println("Got an update for name", name)
		if err := schema.Validate(name, namespace, obj.Body); err != nil {
			// Discard the resource
			continue
		}
		if obj.Metadata.CreateTime != nil {
			var err error
			if createTime, err = types.TimestampFromProto(obj.Metadata.CreateTime); err != nil {
				continue
			}
		}
		configMeta := istio_objs.ConfigMeta{
			Type:              descriptor.Type,
			Group:             descriptor.Group,
			Version:           descriptor.Version,
			Name:              name,
			Namespace:         namespace,
			ResourceVersion:   obj.Metadata.Version,
			CreationTimestamp: createTime,
			Labels:            obj.Metadata.Labels,
			Annotations:       obj.Metadata.Annotations,
			// TODO (sudswas): Change this to take it from config
			Domain: "avi.internal",
		}
		configMeta.QueueByTypes(obj.Body)
	}
	istio_objs.CalculateUpdates(descriptor.Type)
	return nil
}

func extractNameNamespace(metadataName string) (string, string) {
	segments := strings.Split(metadataName, "/")
	if len(segments) == 2 {
		return segments[0], segments[1]
	}
	return "", segments[0]
}
