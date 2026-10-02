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
	// Name is the logical registry key shared by every runtime implementation.
	Name = "devsy-runtime"
	// ProtocolVersion selects the breaking go-plugin application generation.
	ProtocolVersion = 1
)

// Handshake returns a fresh configuration; the cookie is an identity check,
// not authentication or a security boundary.
func Handshake() hplugin.HandshakeConfig {
	return hplugin.HandshakeConfig{ProtocolVersion: ProtocolVersion, MagicCookieKey: "DEVSY_RUNTIME_PLUGIN", MagicCookieValue: "devsy-runtime-v1"}
}

// Runtime bridges one RuntimeDriver implementation to the gRPC-only plugin ABI.
// Server processes set Implementation; client registries leave it unset.
type Runtime struct {
	hplugin.NetRPCUnsupportedPlugin
	Implementation runtimev1.RuntimeDriverServer
}

var _ hplugin.GRPCPlugin = (*Runtime)(nil)

// GRPCServer rejects an unset implementation before registering the service.
func (p *Runtime) GRPCServer(_ *hplugin.GRPCBroker, s *grpc.Server) error {
	if p.Implementation == nil {
		return errors.New("runtime server implementation is required")
	}
	runtimev1.RegisterRuntimeDriverServer(s, p.Implementation)
	return nil
}

// GRPCClient binds the generated client to the connection owned by go-plugin.
func (*Runtime) GRPCClient(_ context.Context, _ *hplugin.GRPCBroker, conn *grpc.ClientConn) (any, error) {
	return runtimev1.NewRuntimeDriverClient(conn), nil
}

// ClientPlugins returns a new registry for gRPC-only clients. The host owns
// process launch, trusted executable resolution, cancellation, and Kill/reap.
func ClientPlugins() map[string]hplugin.Plugin {
	return map[string]hplugin.Plugin{Name: &Runtime{}}
}
