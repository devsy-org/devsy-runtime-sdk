// Package plugin defines the shared go-plugin transport for Runtime Protocol v1.
package plugin

import (
	"context"
	"errors"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	hplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

const (
	Name            = "devsy-runtime"
	ProtocolVersion = 1
)

// Handshake returns a fresh configuration; the cookie is an identity check,
// not authentication or a security boundary.
func Handshake() hplugin.HandshakeConfig {
	return hplugin.HandshakeConfig{ProtocolVersion: ProtocolVersion, MagicCookieKey: "DEVSY_RUNTIME_PLUGIN", MagicCookieValue: "devsy-runtime-v1"}
}

type Runtime struct {
	hplugin.NetRPCUnsupportedPlugin
	Implementation runtimev1.RuntimeDriverServer
}

var _ hplugin.GRPCPlugin = (*Runtime)(nil)

func (p *Runtime) GRPCServer(_ *hplugin.GRPCBroker, s *grpc.Server) error {
	if p.Implementation == nil {
		return errors.New("runtime server implementation is required")
	}
	runtimev1.RegisterRuntimeDriverServer(s, p.Implementation)
	return nil
}

func (*Runtime) GRPCClient(_ context.Context, _ *hplugin.GRPCBroker, conn *grpc.ClientConn) (any, error) {
	return runtimev1.NewRuntimeDriverClient(conn), nil
}

// ClientPlugins returns a new registry for gRPC-only clients. The host owns
// process launch, trusted executable resolution, cancellation, and Kill/reap.
func ClientPlugins() map[string]hplugin.Plugin {
	return map[string]hplugin.Plugin{Name: &Runtime{}}
}
