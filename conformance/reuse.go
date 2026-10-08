package conformance

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func reusePreflight(t *testing.T, options Options) {
	s := open(t, options)
	if !s.info.GetCapabilities().GetReusePreflight() {
		t.Skip("runtime does not advertise reuse preflight")
	}
	s.create(t)
	before, err := s.client.Find(
		s.ctx,
		&runtimev1.FindRequest{WorkspaceId: s.request.GetWorkspaceId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := &runtimev1.ReusePreflightRequest{
		WorkspaceId: s.request.GetWorkspaceId(), RemoteUser: reuseRemoteUser(s.request),
	}
	if _, err := s.client.ReusePreflight(s.ctx, request); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(s.ctx)
	cancel()
	if _, err := s.client.ReusePreflight(ctx, request); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled reuse preflight returned %v", err)
	}
	after, err := s.client.Find(
		s.ctx,
		&runtimev1.FindRequest{WorkspaceId: s.request.GetWorkspaceId()},
	)
	if err != nil || !proto.Equal(before, after) {
		t.Fatalf("reuse preflight changed workspace: %v, %v", after, err)
	}
}

func reuseRemoteUser(request *runtimev1.RunImageRequest) string {
	if request.GetRemoteUser() != "" {
		return request.GetRemoteUser()
	}
	if request.GetUser() != "" {
		return request.GetUser()
	}
	return "root"
}
