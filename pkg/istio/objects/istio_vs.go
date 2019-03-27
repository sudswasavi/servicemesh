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
	"sync"

	"github.com/golang/protobuf/proto"
)

// Everything is in memory for us, so we will keep things typed on resources despite them containing
// generic information. This means that those hydrating these structs should be aware of the types as well.
// Makes a client coupled design, but we are fine with that for now.
type VirtualService struct {
	ConfigMeta
	Spec proto.Message
}

func NewIstioVirtualService(configMeta ConfigMeta, spec proto.Message) *VirtualService {
	vs := &VirtualService{}
	vs.ConfigMeta = configMeta
	vs.Spec = spec
	return vs
}

type VirtualServiceMap struct {
	vsMap    map[string]*VirtualService
	map_lock sync.RWMutex
}

func NewVirtualServiceMap() *VirtualServiceMap {
	vsMap := VirtualServiceMap{}
	vsMap.vsMap = make(map[string]*VirtualService)
	return &vsMap
}

func (v *VirtualServiceMap) GetObjByNameNamespace(key string) (bool, *VirtualService) {
	v.map_lock.RLock()
	defer v.map_lock.RUnlock()
	val, ok := v.vsMap[key]
	if !ok {
		fmt.Println("Key not found in cache")
		return ok, nil
	} else {
		return ok, val
	}
}

func (v *VirtualServiceMap) AddObj(key string, val *VirtualService) {
	// Add a VS object for processing
	v.map_lock.RLock()
	defer v.map_lock.RUnlock()
	v.vsMap[key] = val
}

func (v *VirtualServiceMap) Done(key string) {
	// Remove an object from this queue once processing is done
	v.map_lock.RLock()
	defer v.map_lock.RUnlock()
	delete(v.vsMap, key)
}
