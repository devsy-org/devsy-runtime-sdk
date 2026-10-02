// Package server provides the executable entry point for external runtimes.
package server

import (
	"github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	hplugin "github.com/hashicorp/go-plugin"
)

// Serve runs the go-plugin server. Call from main after parsing plugin arguments.
// Business logs belong on stderr; binary Exec output belongs in gRPC frames.
func Serve(implementation runtimev1.RuntimeDriverServer) {
	hplugin.Serve(&hplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake(),
		VersionedPlugins: map[int]hplugin.PluginSet{
			plugin.ProtocolVersion: {plugin.Name: &plugin.Runtime{Implementation: implementation}},
		},
		GRPCServer: hplugin.DefaultGRPCServer,
	})
}
