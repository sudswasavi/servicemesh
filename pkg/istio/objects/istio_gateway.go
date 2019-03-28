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

/* These structs consist of generic information but we still type them into resource
specific structs for now. This is because, we would want to keep the design modular*/
type Gateway struct {
	ConfigMeta
	Spec proto.Message
}

func NewGateway(configMeta ConfigMeta, spec proto.Message) *Gateway {
	gw := &Gateway{}
	gw.ConfigMeta = configMeta
	gw.Spec = spec
	return gw
}

type GatewayMap struct {
	gwMap    map[string]*Gateway
	map_lock sync.RWMutex
}

func NewGatewayMap() *GatewayMap {
	gwMap := GatewayMap{}
	gwMap.gwMap = make(map[string]*Gateway)
	return &gwMap
}

func (g *GatewayMap) GetObjByNameNamespace(key string) (bool, *Gateway) {
	g.map_lock.RLock()
	defer g.map_lock.RUnlock()
	val, ok := g.gwMap[key]
	if !ok {
		fmt.Println("Key not found in cache")
		return ok, nil
	} else {
		return ok, val
	}
}

func (g *GatewayMap) AddObj(key string, val *Gateway) {
	// Add a gateway object in this Queue for processing.
	g.map_lock.RLock()
	defer g.map_lock.RUnlock()
	g.gwMap[key] = val
}

func (g *GatewayMap) DelObj(key string) {
	// Use this method to delete an object from the Gateway queue once processing is complete
	g.map_lock.RLock()
	defer g.map_lock.RUnlock()
	delete(g.gwMap, key)
}
