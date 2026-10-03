package conformance

import (
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
)

func discovery(t *testing.T, options Options) {
	s := open(t, options)
	s.preflight(t)
	architecture, err := s.client.TargetArchitecture(
		s.ctx,
		&runtimev1.TargetArchitectureRequest{WorkspaceId: s.request.GetWorkspaceId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if architecture.GetArchitecture() != "amd64" && architecture.GetArchitecture() != "arm64" {
		t.Fatalf("noncanonical architecture %q", architecture.GetArchitecture())
	}
}

func lifecycle(t *testing.T, options Options) {
	s := open(t, options)
	s.assertState(t, "")
	s.create(t)
	s.assertState(t, "running")
	for range 2 {
		if _, err := s.client.Stop(
			s.ctx,
			&runtimev1.StopRequest{WorkspaceId: s.request.GetWorkspaceId()},
		); err != nil {
			t.Fatal(err)
		}
		s.assertState(t, "stopped")
	}
	for range 2 {
		if _, err := s.client.Start(
			s.ctx,
			&runtimev1.StartRequest{WorkspaceId: s.request.GetWorkspaceId()},
		); err != nil {
			t.Fatal(err)
		}
		s.assertState(t, "running")
	}
	for range 2 {
		if _, err := s.client.Delete(
			s.ctx,
			&runtimev1.DeleteRequest{WorkspaceId: s.request.GetWorkspaceId()},
		); err != nil {
			t.Fatal(err)
		}
		s.assertState(t, "")
	}
	_, err := s.client.Start(
		s.ctx,
		&runtimev1.StartRequest{WorkspaceId: s.request.GetWorkspaceId()},
	)
	RequireRuntimeError(
		t,
		err,
		runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_NOT_FOUND,
	)
}

func (s *session) assertState(t *testing.T, state string) {
	t.Helper()
	found, err := s.client.Find(
		s.ctx,
		&runtimev1.FindRequest{WorkspaceId: s.request.GetWorkspaceId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if found.GetFound() != (state != "") {
		t.Fatalf("unexpected presence: %v", found)
	}
	if state == "" {
		if found.GetContainer() != nil {
			t.Fatal("absent workspace has container details")
		}
		return
	}
	container := found.GetContainer()
	if container.GetId() == "" || container.GetState().GetStatus() != state {
		t.Fatalf("unexpected container state: %v", container)
	}
}

func (s *session) preflight(t *testing.T) {
	t.Helper()
	if _, err := s.client.Preflight(s.ctx, &runtimev1.PreflightRequest{}); err != nil {
		t.Fatal(err)
	}
	if !s.info.GetCapabilities().GetProvisioningPreflight() {
		return
	}
	if _, err := s.client.ProvisioningPreflight(
		s.ctx,
		&runtimev1.ProvisioningPreflightRequest{},
	); err != nil {
		t.Fatal(err)
	}
}
