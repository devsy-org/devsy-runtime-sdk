package conformance

import (
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/protobuf/proto"
)

func recreateOnRun(t *testing.T, options Options) {
	for _, state := range []string{"running", "stopped"} {
		t.Run(state, func(t *testing.T) {
			s := open(t, options)
			info, err := s.client.Info(s.ctx, &runtimev1.InfoRequest{SupportsRecreateOnRun: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := runtimev1.ValidateInfo(info); err != nil {
				t.Fatal(err)
			}
			if info.GetCapabilities().
				GetRecreateMode() !=
				runtimev1.RecreateMode_RECREATE_MODE_ON_RUN {
				t.Skip("runtime does not advertise recreate on run")
			}
			if s.info.GetCapabilities().
				GetRecreateMode() !=
				runtimev1.RecreateMode_RECREATE_MODE_DELETE {
				t.Fatal("ON_RUN runtime must advertise DELETE without opt-in")
			}
			s.create(t)
			if state == "stopped" {
				if _, err := s.client.Stop(s.ctx, &runtimev1.StopRequest{
					WorkspaceId: s.request.GetWorkspaceId(),
				}); err != nil {
					t.Fatal(err)
				}
			}
			s.assertState(t, state)
			s.checkRecreation(t)
		})
	}
}

func (s *session) checkRecreation(t *testing.T) {
	t.Helper()
	before := s.recreateSnapshot(t)
	_, err := s.client.RunImage(s.ctx, s.request)
	RequireRuntimeError(t, err, runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_ALREADY_EXISTS)
	s.assertRecreateSnapshot(t, before)
	invalid := proto.CloneOf(s.request)
	invalid.AllowRecreate, invalid.Image = true, ""
	_, err = s.client.RunImage(s.ctx, invalid)
	RequireRuntimeError(t, err, runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INVALID_ARGUMENT)
	s.assertRecreateSnapshot(t, before)
	s.checkInvalidRecreateMounts(t, before)
	replacement := proto.CloneOf(s.request)
	replacement.AllowRecreate = true
	const generationKey = "devsy.conformance.recreate-generation"
	if _, exists := before.GetContainer().GetConfig().GetLabels()[generationKey]; exists {
		t.Fatal("adapter workspace already uses the conformance recreation label")
	}
	replacement.Labels = append(replacement.Labels, generationKey+"="+t.Name())
	if _, err := s.client.RunImage(s.ctx, replacement); err != nil {
		t.Fatal(err)
	}
	s.assertState(t, "running")
	after := s.recreateSnapshot(t)
	if after.GetContainer().GetConfig().GetLabels()[generationKey] != t.Name() {
		t.Fatal("authorized replacement did not apply the requested generation")
	}
	replacement.AllowRecreate = false
	_, err = s.client.RunImage(s.ctx, replacement)
	RequireRuntimeError(t, err, runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_ALREADY_EXISTS)
	s.assertRecreateSnapshot(t, after)
}

func (s *session) checkInvalidRecreateMounts(t *testing.T, before *runtimev1.FindResponse) {
	t.Helper()
	for _, test := range []struct {
		name           string
		workspaceMount *runtimev1.Mount
		mount          *runtimev1.Mount
	}{
		{
			name: "unsupported workspace mount",
			workspaceMount: &runtimev1.Mount{
				Type:   runtimev1.MountType(99),
				Target: "/unsupported-workspace-mount",
			},
		},
		{
			name: "unsupported additional mount",
			mount: &runtimev1.Mount{
				Type:   runtimev1.MountType(99),
				Target: "/unsupported-additional-mount",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalidMount := proto.CloneOf(s.request)
			invalidMount.AllowRecreate = true
			if test.workspaceMount != nil {
				invalidMount.WorkspaceMount = proto.CloneOf(test.workspaceMount)
			} else {
				invalidMount.Mounts = append(invalidMount.Mounts, proto.CloneOf(test.mount))
			}
			if invalidMount.GetImage() == "" {
				t.Fatal("invalid-mount request must retain a nonempty image")
			}
			_, err := s.client.RunImage(s.ctx, invalidMount)
			RequireRuntimeError(t, err, runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNSUPPORTED)
			s.assertRecreateSnapshot(t, before)
		})
	}
}

func (s *session) recreateSnapshot(t *testing.T) *runtimev1.FindResponse {
	t.Helper()
	found, err := s.client.Find(
		s.ctx,
		&runtimev1.FindRequest{WorkspaceId: s.request.GetWorkspaceId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found.GetFound() {
		t.Fatal("recreation workspace disappeared")
	}
	return found
}

func (s *session) assertRecreateSnapshot(t *testing.T, before *runtimev1.FindResponse) {
	t.Helper()
	if after := s.recreateSnapshot(t); !proto.Equal(before, after) {
		t.Fatalf("rejected recreation mutated workspace: before=%v after=%v", before, after)
	}
}
