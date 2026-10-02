package runtimev1_test

import (
	"strings"
	"testing"

	v1 "github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func validInfo() *v1.InfoResponse {
	return &v1.InfoResponse{ApiMajor: 1, DriverName: "fake", DriverVersion: "1.0", RuntimeName: "backend", Capabilities: &v1.Capabilities{RecreateMode: v1.RecreateMode_RECREATE_MODE_STOP}}
}

func TestValidateInfo(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*v1.InfoResponse)
		want   string
	}{
		{"future minor", func(i *v1.InfoResponse) { i.ApiMinor = 99 }, ""},
		{"different major", func(i *v1.InfoResponse) { i.ApiMajor = 2 }, "install a runtime supporting API major 1"},
		{"empty name", func(i *v1.InfoResponse) { i.DriverName = "" }, "driver name"},
		{"missing capabilities", func(i *v1.InfoResponse) { i.Capabilities = nil }, "capabilities"},
		{"unknown recreate", func(i *v1.InfoResponse) { i.Capabilities.RecreateMode = 99 }, "recreate mode"},
		{"unknown mount", func(i *v1.InfoResponse) { i.Capabilities.MountTypes = []v1.MountType{99} }, "mount type"},
		{"supported mounts", func(i *v1.InfoResponse) {
			i.Capabilities.MountTypes = []v1.MountType{v1.MountType_MOUNT_TYPE_BIND, v1.MountType_MOUNT_TYPE_VOLUME, v1.MountType_MOUNT_TYPE_TMPFS}
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := validInfo()
			tc.mutate(info)
			err := v1.ValidateInfo(info)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
	if err := v1.ValidateInfo(nil); err == nil {
		t.Fatal("nil Info accepted")
	}
}

func TestOptionalFlagsPreservePresence(t *testing.T) {
	input := &v1.RunImageRequest{Privileged: new(false)}
	data, err := proto.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	output := new(v1.RunImageRequest)
	if err := proto.Unmarshal(data, output); err != nil {
		t.Fatal(err)
	}
	if output.Privileged == nil || *output.Privileged || output.Init != nil {
		t.Fatal("unset and explicit false flags were conflated")
	}
}

func TestStructuredErrorDetails(t *testing.T) {
	detail := &v1.RuntimeError{Code: v1.RuntimeErrorCode_RUNTIME_ERROR_CODE_NOT_FOUND, Message: "workspace absent", RuntimeMessage: "backend diagnostic", Retryable: false}
	s, err := status.New(codes.NotFound, detail.Message).WithDetails(detail)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := proto.Marshal(s.Proto())
	if err != nil {
		t.Fatal(err)
	}
	decoded := proto.Clone(s.Proto())
	proto.Reset(decoded)
	if err := proto.Unmarshal(wire, decoded); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(s.Proto(), decoded) {
		t.Fatal("typed runtime error was not preserved")
	}
	recovered, ok := s.Details()[0].(*v1.RuntimeError)
	if !ok || !proto.Equal(recovered, detail) {
		t.Fatal("typed runtime detail was not recoverable")
	}
}
