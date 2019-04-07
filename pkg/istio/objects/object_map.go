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
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/avinetworks/servicemesh/pkg/utils"
	"github.com/golang/protobuf/proto"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
)

// This is a basic object that is used to store istio object information.

const (
	NotFound string = "Not-found"
	ADD      string = "ADD"
	DELETE   string = "DELETE"
	UPDATE   string = "UPDATE"
)

type IstioObject struct {
	ConfigMeta
	Spec proto.Message
}

type GatewayToVsMap struct {
	// One gateway can have multiple VSes.
	GwVSMap map[string][]string
	// This flag indicates if gateway for a given key is already created or not.
	// This helps the VS decide on whether it should proceed with patching the VS.
	GwCreated  map[string]bool
	GW_VS_lock sync.RWMutex
}

func NewGatewayToVsMap() *GatewayToVsMap {
	gwVsMap := &GatewayToVsMap{}
	gwVsMap.GwVSMap = make(map[string][]string)
	gwVsMap.GwCreated = make(map[string]bool)
	return gwVsMap
}

func (gvs *GatewayToVsMap) IsGatewayCreated(gwName string) bool {
	gvs.GW_VS_lock.RLock()
	defer gvs.GW_VS_lock.RUnlock()
	val, ok := gvs.GwCreated[gwName]
	if ok {
		return val
	} else {
		return false
	}
}

func (gvs *GatewayToVsMap) AddVsToGateway(vsName string, gwName string) {
	gvs.GW_VS_lock.RLock()
	defer gvs.GW_VS_lock.RUnlock()
	_, ok := gvs.GwVSMap[gwName]
	// If the gateway name exists - just append to it. Else create an entry and add the VS.
	if ok {
		gvs.GwVSMap[gwName] = append(gvs.GwVSMap[gwName], vsName)
	} else {
		gvs.GwVSMap[gwName] = []string{vsName}
	}
}

func (gvs *GatewayToVsMap) GetVsFromGateway(gwName string) ([]string, bool) {
	gvs.GW_VS_lock.RLock()
	defer gvs.GW_VS_lock.RUnlock()
	val, ok := gvs.GwVSMap[gwName]
	// If the gateway name exists - return the corresponding VSes.
	if ok {
		return val, true
	} else {
		return nil, false
	}
}

func (gvs *GatewayToVsMap) RemoveVsToGateway(vsName string, gwName string) {
	gvs.GW_VS_lock.RLock()
	defer gvs.GW_VS_lock.RUnlock()
	// If the Gateway entry exists, then delete the VS from the gateway, if not - don't do anything.
	val, ok := gvs.GwVSMap[gwName]
	if ok {
		Remove(val, vsName)
	}
}

func (gvs *GatewayToVsMap) PopulateGWCreated(gwName string) {
	gvs.GW_VS_lock.RLock()
	defer gvs.GW_VS_lock.RUnlock()
	// If the gateway is created, let's mark the map against it to true.
	gvs.GwCreated[gwName] = true
	utils.AviLog.Trace.Println("Added Gateway", gvs.GwCreated)
}

func (gvs *GatewayToVsMap) RemoveGWCreated(gwName string) {
	gvs.GW_VS_lock.RLock()
	defer gvs.GW_VS_lock.RUnlock()
	// If the gateway is removed, let's first check if there's an entry in the GWVsMap.
	// If the entry exists, then we should mark the GwCreated flag to false, else we remove
	// the entry from GwCreated
	gvs.GW_VS_lock.RLock()
	defer gvs.GW_VS_lock.RUnlock()
	_, ok := gvs.GwVSMap[gwName]
	if ok {
		gvs.GwCreated[gwName] = false
	} else {
		delete(gvs.GwCreated, gwName)
	}
}

func Remove(s []string, r string) []string {
	for i, v := range s {
		if v == r {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func NewIstioObject(configMeta ConfigMeta, spec proto.Message) *IstioObject {
	obj := &IstioObject{}
	obj.ConfigMeta = configMeta
	obj.Spec = spec
	return obj
}

// Map Key is - namespace:object_name. This stores already discovered Istio Object Map.
type IstioObjectMap struct {
	ObjMap   map[string]*IstioObject
	map_lock sync.RWMutex
}

func NewIstioObjectMap() *IstioObjectMap {
	objMap := IstioObjectMap{}
	objMap.ObjMap = make(map[string]*IstioObject)
	return &objMap
}

func (c *IstioObjectMap) GetObjByNameNamespace(key string) (bool, *IstioObject) {
	c.map_lock.RLock()
	defer c.map_lock.RUnlock()
	val, ok := c.ObjMap[key]
	if !ok {
		return ok, nil
	} else {
		return ok, val
	}
}

func (o *IstioObjectMap) AddObj(key string, val *IstioObject) {
	// Add a gateway object in this Queue for processing.
	o.map_lock.RLock()
	defer o.map_lock.RUnlock()
	o.ObjMap[key] = val
}

func (o *IstioObjectMap) GetObj(key string) *IstioObject {
	val, ok := o.ObjMap[key]
	if !ok {
		// key not found
		return nil
	}
	return val
}

/* This has the mechanics to perform actions on the Istio Object. We keep a map to
figure out what are the operations to be performed against an Istio object.*/
type IstioObjectOpsController struct {
	opsMap               map[string][]string
	opsLock              sync.RWMutex
	workqueue            []workqueue.RateLimitingInterface
	avi_obj_cache        *utils.AviObjCache
	avi_rest_client_pool *utils.AviRestClientPool
	worker_id_mutex      sync.Mutex
	worker_id            uint32
	num_workers          uint32
}

func NewIstioObjectOpsController(avi_obj_cache *utils.AviObjCache, avi_rest_client_pool *utils.AviRestClientPool) *IstioObjectOpsController {
	opsCont := &IstioObjectOpsController{}
	opsCont.opsMap = make(map[string][]string)
	//opsCont.workqueue = workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), fmt.Sprintf("avi-%s", "temp"))
	opsCont.avi_obj_cache = avi_obj_cache
	opsCont.avi_rest_client_pool = avi_rest_client_pool
	opsCont.num_workers = utils.NumWorkers
	opsCont.worker_id = (uint32(1) << utils.NumWorkers) - 1
	//TODO (sudswas): Do we need to worry about shutting this down based on SIGTERM?
	// Go routine spun up per object type of Istio.
	opsCont.workqueue = make([]workqueue.RateLimitingInterface, utils.NumWorkers)
	for i := uint32(0); i < utils.NumWorkers; i++ {
		opsCont.workqueue[i] = workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), fmt.Sprintf("avi-%d", i))
	}
	return opsCont
}

func Bkt(key string, num_workers uint32) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	bkt := h.Sum32() & (num_workers - 1)
	return bkt
}

/* This method keeps adding operations against a given key.
The draining go routine, will ensure that operations are processed and eventually
the actions are removed from the ops queue. But till that happens, we should also
be able to optimize the cost of repeated operations to fewer operations for us to
process*/
func (o *IstioObjectOpsController) AddOps(key string, oper string) {
	o.opsLock.RLock()
	defer o.opsLock.RUnlock()
	var bkt uint32
	hashProcessed := false
	// Let's add the key to the work queue.
	// Let's see if the key corresponds to a VS and if so - we will shard it to the same Gateway queue.
	obj_type_ns := strings.SplitN(key, "/", 2)
	if len(obj_type_ns) != 2 {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return
	}
	if obj_type_ns[0] == ISTIOVIRTUALSERVICE {
		// Read the object and check the gateway name
		vsObj := NewVsMap.GetObj(obj_type_ns[1])
		gateways := GetGatewayForVS(vsObj)
		/* The key of a Virtual Service may impact 1 or may gateways. Let's say a virtual service impacts
		3 gateways simultaneously. In such case, 3 AVI VSes will be impacted by one VS key. Also we won't be
		able to decide which queue to shard this VS on because we would want to shard the Istio VS on the gateway
		name. In order to overcome this complicated situation, we would have keys of the format:
		 - ns:VsName/GatewayName. Right now the assumption is that the gateway exists in the same namespace as the VS.
		So if there are 2 Gateways that one VS impacts, the keys would be:
		 - ns:VsName/GW1
		 - ns:VsName/GW2 */
		for _, gateway := range gateways {
			bkt = Bkt(gateway, o.num_workers)
			key = key + "/" + gateway
			o.workqueue[bkt].AddRateLimited(key)
			hashProcessed = true
		}
	}
	if !hashProcessed {
		// Default case.
		bkt = Bkt(key, o.num_workers)
		o.workqueue[bkt].AddRateLimited(key)
	}
	switch oper {
	case ADD:
		o.opsMap[key] = append(o.opsMap[key], oper)
	case DELETE:
		last_oper := o.opsMap[key][len(o.opsMap[key])-1]
		if last_oper != DELETE {
			//If the last operation is ADD/UPDATE but it was deleted after wards - we just need to process DELETE.
			o.opsMap[key] = []string{}
			o.opsMap[key] = append(o.opsMap[key], oper)
		}
	case UPDATE:
		// There are two possibilities with an UPDATE: [ADD] existed and now UPDATE came- Consolidate to one [ADD] - that means discard this op.
		// [UPDATE] existed, and now another UPDATE came - Consolidate to one [UPDATE]. That also means discard this update.
		// Else if there are no pending operations: [UPDATE] that is length of ops is 0.
		// Note UPDATE cannot come after DELETE.
		if len(o.opsMap[key]) == 0 {
			o.opsMap[key] = append(o.opsMap[key], oper)
		}
	}
}

func (o *IstioObjectOpsController) GetOp(key string) string {
	o.opsLock.RLock()
	defer o.opsLock.RUnlock()
	val, ok := o.opsMap[key]
	if ok {
		if len(val) == 1 {
			return val[0]
		} else {
			utils.AviLog.Warning.Printf("More than one operation found for the object %s", key)
		}
	} else {
		utils.AviLog.Warning.Printf("No operation found for key %s", key)
	}
	return NotFound
}

func (o *IstioObjectOpsController) RemoveOp(key string) {
	o.opsLock.RLock()
	defer o.opsLock.RUnlock()
	_, ok := o.opsMap[key]
	if ok {
		// Clear the ops map if the item is processed.
		delete(o.opsMap, key)
	} else {
		utils.AviLog.Warning.Printf("No operation found for key %s", key)
	}
}

func (o *IstioObjectOpsController) Start(stopCh <-chan struct{}) error {
	defer runtime.HandleCrash()

	utils.AviLog.Info.Print("Starting  Istio workers")
	// Launch two workers to process Foo resources
	for i := uint32(0); i < o.num_workers; i++ {
		go wait.Until(o.runWorker, time.Second, stopCh)
	}

	utils.AviLog.Info.Print("Started Istio workers")
	<-stopCh
	return nil
}

func (o *IstioObjectOpsController) StopWorkers(stopCh <-chan struct{}) {
	for i := uint32(0); i < o.num_workers; i++ {
		defer o.workqueue[i].ShutDown()
	}
}

func (o *IstioObjectOpsController) runWorker() {
	worker_id := uint32(0xffffffff)
	o.worker_id_mutex.Lock()
	for i := uint32(0); i < o.num_workers; i++ {
		if ((uint32(1) << i) & o.worker_id) != 0 {
			worker_id = i
			o.worker_id = o.worker_id & ^(uint32(1) << i)
			break
		}
	}
	o.worker_id_mutex.Unlock()
	utils.AviLog.Info.Printf("Starting Istio Worker with ID:  %d", worker_id)
	for o.processNextWorkItem(worker_id) {
	}
	o.worker_id_mutex.Lock()
	o.worker_id = o.worker_id | (uint32(1) << worker_id)
	o.worker_id_mutex.Unlock()
	utils.AviLog.Info.Printf("Istio Worker ID %d restarting", worker_id)

}

func (o *IstioObjectOpsController) processNextWorkItem(worker_id uint32) bool {
	obj, shutdown := o.workqueue[worker_id].Get()
	if shutdown {
		return false
	}
	var ok bool
	var ev string
	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer o.workqueue[worker_id].Done(obj)
		if ev, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			o.workqueue[worker_id].Forget(obj)
			runtime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncToAvi, passing it the ev resource to be synced.
		if err := o.syncToAvi(ev); err != nil {
			o.workqueue[worker_id].Forget(obj)
			return nil
		}

		return nil
	}(obj)
	if err != nil {
		runtime.HandleError(err)
		return true
	}

	return true
}

func (o *IstioObjectOpsController) syncToAvi(key string) error {
	// Perform CRUD to AVI by first looking up the latest object from IstioObjectMap and it's corresponding action viz. ADD, UPDATE, DELETE
	// TODO (sudswas): Handle the errors/re-queing logic.
	// Key is of the form: ObjectType/Namespace:ObjectName
	obj_type_ns := strings.SplitN(key, "/", 2)
	if obj_type_ns[0] == ISTIOVIRTUALSERVICE {
		o.ProcessIstioVirtualService(obj_type_ns[1], VsGwMap)
	} else if obj_type_ns[0] == GATEWAY {
		// Gateway does not interact with the service registry.
		if OpsCtrl.GetOp(key) == ADD {
			o.ProcessGatewayAdd(NewGwMap.GetObj(obj_type_ns[1]), VsGwMap)
			OpsCtrl.RemoveOp(key)
		} else if OpsCtrl.GetOp(key) == DELETE {
			utils.AviLog.Info.Println("Not implemented.")
		} else if OpsCtrl.GetOp(key) == UPDATE {
			utils.AviLog.Info.Println("Not Implemented. ", key)
		}

	}
	return nil
}
