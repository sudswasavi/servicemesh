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
	"context"
	"fmt"
	"net/url"
	"sync"

	"github.com/avinetworks/servicemesh/pkg/utils"
	"google.golang.org/grpc"
	mcpapi "istio.io/api/mcp/v1alpha1"
	"istio.io/istio/pkg/mcp/client"
	"istio.io/istio/pkg/mcp/configz"
	"istio.io/istio/pkg/mcp/monitoring"
	"istio.io/istio/pkg/mcp/sink"
)

const (

	// DefaultMCPMaxMsgSize is the default maximum message size
	DefaultMCPMaxMsgSize = 1024 * 1024 * 4
)

type MCPClient struct {
	MCPServerAddrs []string
	startFuncs     []startFunc
}

func (c *MCPClient) Start(stop <-chan struct{}) error {
	// Now start all of the components.
	for _, fn := range c.startFuncs {
		if err := fn(stop); err != nil {
			return err
		}
	}

	return nil
}

func (c *MCPClient) addStartFunc(fn startFunc) {
	c.startFuncs = append(c.startFuncs, fn)
}

type startFunc func(stop <-chan struct{}) error

func (c *MCPClient) InitMCPClient(avi_obj_cache *utils.AviObjCache, avi_rest_client_pool *utils.AviRestClientPool, stopCh <-chan struct{}) (*Controller, error) {
	var mcpController *Controller
	clientNodeID := ""
	collections := make([]sink.CollectionOptions, len(IstioConfigTypes))
	for i, model := range IstioConfigTypes {
		collections[i] = sink.CollectionOptions{
			Name:        model.Collection,
			Incremental: true,
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	var clients []*client.Client
	var conns []*grpc.ClientConn

	reporter := monitoring.NewStatsContext("gocontroller/mcp/sink")

	for _, addr := range c.MCPServerAddrs {
		u, err := url.Parse(addr)
		if err != nil {
			cancel()
			return nil, err
		}
		utils.AviLog.Info.Println("The MCP server address", u.Host)
		securityOption := grpc.WithInsecure()
		msgSizeOption := grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(DefaultMCPMaxMsgSize))
		conn, err := grpc.DialContext(ctx, u.Host, securityOption, msgSizeOption)
		if err != nil {
			fmt.Errorf("Unable to dial MCP Server %q: %v", u.Host, err)
			cancel()
			return nil, err
		}
		cl := mcpapi.NewAggregatedMeshConfigServiceClient(conn)
		mcpController = NewController(avi_obj_cache, avi_rest_client_pool, stopCh)
		sinkOptions := &sink.Options{
			CollectionOptions: collections,
			Updater:           mcpController,
			ID:                clientNodeID,
			Reporter:          reporter,
		}
		mcpClient := client.New(cl, sinkOptions)
		configz.Register(mcpClient)
		utils.AviLog.Info.Println("Successfully registered the MCP client")
		clients = append(clients, mcpClient)
		conns = append(conns, conn)
	}

	c.addStartFunc(func(stop <-chan struct{}) error {
		var wg sync.WaitGroup

		for i := range clients {
			client := clients[i]
			wg.Add(1)
			go func() {
				client.Run(ctx)
				wg.Done()
			}()
		}

		go func() {
			<-stop

			// Stop the MCP clients and any pending connection.
			cancel()

			// Close all of the open grpc connections once the mcp
			// client(s) have fully stopped.
			wg.Wait()
			for _, conn := range conns {
				_ = conn.Close() // nolint: errcheck
			}

			reporter.Close()
		}()

		return nil
	})
	return mcpController, nil
}
