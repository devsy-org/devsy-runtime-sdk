package fake_test

import (
	"bytes"
	"testing"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestLegacyInfoDescriptorIgnoresOptIn(t *testing.T) {
	// API 1.2 InfoRequest is exactly an empty proto3 message at df5943d.
	// This separate descriptor has no new field, unlike a same-schema round trip.
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: new("legacy_info.proto"), Syntax: new("proto3"),
		Package:     new("devsy.runtime.v1"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: new("InfoRequest")}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy := dynamicpb.NewMessage(file.Messages().Get(0))
	wire, err := proto.Marshal(&runtimev1.InfoRequest{SupportsRecreateOnRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(wire, legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Descriptor().Fields().Len() != 0 || !bytes.Equal(legacy.GetUnknown(), wire) {
		t.Fatal("new opt-in was not retained as an unrecognized legacy field")
	}
	assertLegacyHostDoesNotOptIn(t, legacy)
}

func assertLegacyHostDoesNotOptIn(t *testing.T, legacy *dynamicpb.Message) {
	t.Helper()
	// An old host starts with its own empty request, not an echoed unknown payload.
	legacy.Reset()
	wire, err := proto.Marshal(legacy)
	if err != nil || len(wire) != 0 {
		t.Fatalf("empty legacy discovery payload: %x, %v", wire, err)
	}
	current := new(runtimev1.InfoRequest)
	if err := proto.Unmarshal(wire, current); err != nil {
		t.Fatal(err)
	}
	if current.GetSupportsRecreateOnRun() {
		t.Fatal("old host unexpectedly opted into ON_RUN")
	}
}
