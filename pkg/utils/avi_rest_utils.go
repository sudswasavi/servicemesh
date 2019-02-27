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

package utils

import (
	"errors"
	"fmt"

	"github.com/avinetworks/sdk/go/clients"
	"github.com/avinetworks/sdk/go/session"
	"github.com/davecgh/go-spew/spew"
)

type AviRestClientPool struct {
	AviClient []*clients.AviClient
}

func NewAviRestClientPool(num uint32, api_ep string, username string,
	password string) (*AviRestClientPool, error) {
	var p AviRestClientPool

	for i := uint32(0); i < num; i++ {
		aviClient, err := clients.NewAviClient(api_ep, username,
			session.SetPassword(password), session.SetInsecure)
		if err != nil {
			AviLog.Warning.Printf("NewAviClient returned err %v", err)
			return &p, err
		}

		p.AviClient = append(p.AviClient, aviClient)
	}

	return &p, nil
}

func (p *AviRestClientPool) AviRestOperate(c *clients.AviClient, rest_ops []*RestOp) error {
	for i, op := range rest_ops {
		SetTenant := session.SetTenant(op.Tenant)
		SetTenant(c.AviSession)
		SetVersion := session.SetVersion(op.Version)
		SetVersion(c.AviSession)
		switch op.Method {
		case RestPost:
			op.Err = c.AviSession.Post(op.Path, op.Obj, &op.Response)
		case RestPut:
			op.Err = c.AviSession.Put(op.Path, op.Obj, &op.Response)
		case RestGet:
			op.Err = c.AviSession.Get(op.Path, &op.Response)
		case RestPatch:
			op.Err = c.AviSession.Patch(op.Path, op.Obj, op.PatchOp,
				&op.Response)
		case RestDelete:
			op.Err = c.AviSession.Delete(op.Path)
		default:
			AviLog.Error.Printf("Unknown RestOp %v", op.Method)
			op.Err = fmt.Errorf("Unknown RestOp %v", op.Method)
		}
		if op.Err != nil {
			AviLog.Warning.Printf(`RestOp method %v path %v tenant %v Obj %s 
                    returned err %v`, op.Method, op.Path, op.Tenant,
				spew.Sprint(op.Obj), Stringify(op.Err))
			for j := i + 1; j < len(rest_ops); j++ {
				rest_ops[j].Err = errors.New("Aborted due to prev error")
			}
			// Wrap the error into a websync error.
			err := &WebSyncError{err: op.Err, operation: string(op.Method)}
			return err
		} else {
			AviLog.Info.Printf(`RestOp method %v path %v tenant %v response %v`,
				op.Method, op.Path, op.Tenant, Stringify(op.Response))
		}
	}
	return nil
}

func AviModelToUrl(model string) string {
	switch model {
	case "Pool":
		return "/api/pool"
	case "VirtualService":
		return "/api/virtualservice"
	default:
		AviLog.Warning.Printf("Unknown model %v", model)
		return ""
	}
}
