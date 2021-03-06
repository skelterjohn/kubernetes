/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"net"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/proxy"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/proxy/config"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/exec"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/iptables"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/version/verflag"
	"github.com/coreos/go-etcd/etcd"
	"github.com/golang/glog"
)

var (
	etcdServerList util.StringList
	etcdConfigFile = flag.String("etcd_config", "", "The config file for the etcd client. Mutually exclusive with -etcd_servers")
	bindAddress    = util.IP(net.ParseIP("0.0.0.0"))
	clientConfig   = &client.Config{}
)

func init() {
	client.BindClientConfigFlags(flag.CommandLine, clientConfig)
	flag.Var(&etcdServerList, "etcd_servers", "List of etcd servers to watch (http://ip:port), comma separated (optional). Mutually exclusive with -etcd_config")
	flag.Var(&bindAddress, "bind_address", "The IP address for the proxy server to serve on (set to 0.0.0.0 for all interfaces)")
}

func main() {
	flag.Parse()
	util.InitLogs()
	defer util.FlushLogs()

	verflag.PrintAndExitIfRequested()

	serviceConfig := config.NewServiceConfig()
	endpointsConfig := config.NewEndpointsConfig()

	// define api config source
	if clientConfig.Host != "" {
		glog.Infof("Using api calls to get config %v", clientConfig.Host)
		client, err := client.New(clientConfig)
		if err != nil {
			glog.Fatalf("Invalid API configuration: %v", err)
		}
		config.NewSourceAPI(
			client.Services(api.NamespaceAll),
			client.Endpoints(api.NamespaceAll),
			30*time.Second,
			serviceConfig.Channel("api"),
			endpointsConfig.Channel("api"),
		)
	} else {

		var etcdClient *etcd.Client

		// Set up etcd client
		if len(etcdServerList) > 0 {
			// Set up logger for etcd client
			etcd.SetLogger(util.NewLogger("etcd "))
			etcdClient = etcd.NewClient(etcdServerList)
		} else if *etcdConfigFile != "" {
			// Set up logger for etcd client
			etcd.SetLogger(util.NewLogger("etcd "))
			var err error
			etcdClient, err = etcd.NewClientFromFile(*etcdConfigFile)

			if err != nil {
				glog.Fatalf("Error with etcd config file: %v", err)
			}
		}

		// Create a configuration source that handles configuration from etcd.
		if etcdClient != nil {
			glog.Infof("Using etcd servers %v", etcdClient.GetCluster())

			config.NewConfigSourceEtcd(etcdClient,
				serviceConfig.Channel("etcd"),
				endpointsConfig.Channel("etcd"))
		}
	}

	protocol := iptables.ProtocolIpv4
	if net.IP(bindAddress).To4() == nil {
		protocol = iptables.ProtocolIpv6
	}
	loadBalancer := proxy.NewLoadBalancerRR()
	proxier := proxy.NewProxier(loadBalancer, net.IP(bindAddress), iptables.New(exec.New(), protocol))
	// Wire proxier to handle changes to services
	serviceConfig.RegisterHandler(proxier)
	// And wire loadBalancer to handle changes to endpoints to services
	endpointsConfig.RegisterHandler(loadBalancer)

	// Just loop forever for now...
	proxier.SyncLoop()
}
