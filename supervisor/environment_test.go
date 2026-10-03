package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"
)

const (
	environmentCanary = "__env_probe_secret__"
	argumentCanary    = "__argv_probe_secret__"
)

type environmentPolicy struct {
	skipHost  bool
	overrides []string
	want      map[string]string
}

func TestOwnedRuntimeEnvironmentPolicy(t *testing.T) {
	root, err := os.MkdirTemp("", "ep-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	inherited := compatibilityEnvironment(root)
	for key, value := range inherited {
		t.Setenv(key, value)
	}
	cases := map[string]environmentPolicy{
		"inherited": {want: inherited},
		"overrides": {
			overrides: []string{"HTTP_PROXY=", "DOCKER_CONTEXT=provider-context"},
			want:      overrideEnvironment(inherited),
		},
		"explicit-allowlist": {
			skipHost: true, overrides: []string{"NO_PROXY=" + inherited["NO_PROXY"]},
			want: map[string]string{"NO_PROXY": inherited["NO_PROXY"]},
		},
	}
	for name, policy := range cases {
		t.Run(name, func(t *testing.T) { checkEnvironmentPolicy(t, root, policy) })
	}
}

func compatibilityEnvironment(root string) map[string]string {
	values := make(map[string]string, len(compatibilityKeys))
	for _, key := range compatibilityKeys {
		values[key] = "fixture " + key + " λ"
	}
	values["HTTP_PROXY"] = "http://" + environmentCanary + "@proxy.invalid:3128"
	values["HTTPS_PROXY"] = values["HTTP_PROXY"]
	values["NO_PROXY"] = "localhost,127.0.0.1,::1"
	// These directories exist before environment mutation, including Windows TEMP/TMP.
	for _, key := range []string{"TMPDIR", "TMP", "TEMP", "PATH"} {
		values[key] = root
	}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "DOCKER_CONFIG"} {
		values[key] = filepath.Join(root, key+" config λ")
	}
	values["DEVSY_ENV_PROBE_SECRET"] = environmentCanary
	return values
}

func overrideEnvironment(base map[string]string) map[string]string {
	values := make(map[string]string, len(base))
	maps.Copy(values, base)
	values["HTTP_PROXY"] = ""
	values["DOCKER_CONTEXT"] = "provider-context"
	return values
}

func environmentClient(
	t *testing.T,
	root string,
	policy environmentPolicy,
) (*hplugin.Client, *environmentDiagnostics) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := &environmentDiagnostics{}
	overrides := append([]string{}, policy.overrides...)
	overrides = append(
		overrides,
		sdkplugin.Handshake().MagicCookieKey+"=stale-cookie",
		"PLUGIN_PROTOCOL_VERSIONS=99",
		"PLUGIN_CLIENT_CERT=stale-certificate",
		"PLUGIN_MULTIPLEX_GRPC=false",
		"PLUGIN_MIN_PORT=1",
		"PLUGIN_MAX_PORT=1",
		"PLUGIN_UNIX_SOCKET_DIR="+filepath.Join(root, "stale"),
	)
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: sdkplugin.Handshake(),
		VersionedPlugins: map[int]hplugin.PluginSet{
			sdkplugin.ProtocolVersion: sdkplugin.ClientPlugins(),
		},
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
		RunnerFunc: Runner(Options{
			SupervisorBinary: binary,
			SupervisorArgs:   []string{supervisorFixtureRole},
			RuntimeBinary:    binary,
			Args:             []string{"--helper-role=environment", argumentCanary},
			Env:              overrides,
		}),
		SkipHostEnv:         policy.skipHost,
		AutoMTLS:            true,
		GRPCBrokerMultiplex: true,
		MinPort:             20000,
		MaxPort:             30000,
		StartTimeout:        15 * time.Second,
		Logger:              hclog.NewNullLogger(),
		Stderr:              diagnostics,
		SyncStderr:          diagnostics,
		UnixSocketConfig:    &hplugin.UnixSocketConfig{TempDir: root},
	})
	t.Cleanup(client.Kill)
	return client, diagnostics
}

func checkEnvironmentPolicy(t *testing.T, root string, policy environmentPolicy) {
	t.Helper()
	client, diagnostics := environmentClient(t, root, policy)
	report := environmentRPC(t, client)
	for role, values := range map[string]map[string]environmentEntry{"plugin": report.Plugin, "child": report.Child} {
		assertCompatibility(t, role, values, policy.want)
		assertTransportEnvironment(t, values)
	}
	client.Kill()
	if !client.Exited() {
		t.Fatal("environment supervisor was not reaped")
	}
	if !bytes.Contains(diagnostics.snapshot(), []byte("environment fixture serving")) {
		t.Fatal("fixture diagnostics were not captured")
	}
	for _, secret := range []string{environmentCanary, argumentCanary} {
		if bytes.Contains(diagnostics.snapshot(), []byte(secret)) {
			t.Fatal("diagnostics exposed a secret canary")
		}
	}
}

func environmentRPC(t *testing.T, client *hplugin.Client) environmentReport {
	t.Helper()
	rpc, err := client.Client()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := rpc.Dispense(sdkplugin.Name)
	if err != nil {
		t.Fatal(err)
	}
	driver, ok := raw.(runtimev1.RuntimeDriverClient)
	if !ok {
		t.Fatal("unexpected environment fixture client")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	stream, err := driver.Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sendEnvironmentStart(t, stream)
	return readEnvironmentReport(t, stream)
}

func sendEnvironmentStart(t *testing.T, s runtimev1.RuntimeDriver_ExecClient) {
	t.Helper()
	if err := s.Send(&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_Start{
		Start: &runtimev1.ExecStart{Argv: []string{"inspect"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(&runtimev1.ExecClientMessage{Payload: &runtimev1.ExecClientMessage_CloseStdin{
		CloseStdin: &runtimev1.CloseStdin{},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseSend(); err != nil {
		t.Fatal(err)
	}
}

func readEnvironmentReport(t *testing.T, s runtimev1.RuntimeDriver_ExecClient) environmentReport {
	t.Helper()
	frame, err := s.Recv()
	if err != nil {
		t.Fatal(err)
	}
	var report environmentReport
	if err := json.Unmarshal(frame.GetStdout().GetData(), &report); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.Recv()
	if err != nil || terminal.GetExit() == nil {
		t.Fatalf("missing environment exit: %v", err)
	}
	if terminal.GetExit().GetExitCode() != 0 || terminal.GetExit().GetSignal() != "" {
		t.Fatal("environment command failed")
	}
	if _, err := s.Recv(); err != io.EOF {
		t.Fatalf("environment completion: %v", err)
	}
	return report
}

func assertCompatibility(
	t *testing.T,
	role string,
	got map[string]environmentEntry,
	want map[string]string,
) {
	t.Helper()
	for _, key := range compatibilityKeys {
		expected, present := want[key]
		actual := got[key]
		if actual.Present != present || !bytes.Equal(actual.Value, []byte(expected)) {
			t.Fatalf("%s compatibility setting changed: %s", role, key)
		}
	}
}

func assertTransportEnvironment(t *testing.T, got map[string]environmentEntry) {
	t.Helper()
	expected := map[string]string{
		sdkplugin.Handshake().MagicCookieKey: sdkplugin.Handshake().MagicCookieValue,
		"PLUGIN_PROTOCOL_VERSIONS":           "1",
		"PLUGIN_MULTIPLEX_GRPC":              "true",
		"PLUGIN_MIN_PORT":                    "20000",
		"PLUGIN_MAX_PORT":                    "30000",
	}
	for key, value := range expected {
		if !got[key].Present || string(got[key].Value) != value {
			t.Fatalf("transport metadata overridden: %s", key)
		}
	}
	if !bytes.HasPrefix(got["PLUGIN_CLIENT_CERT"].Value, []byte("-----BEGIN CERTIFICATE-----")) {
		t.Fatal("client certificate lost")
	}
	if !got["PLUGIN_UNIX_SOCKET_DIR"].Present {
		t.Fatal("socket metadata lost")
	}
}
