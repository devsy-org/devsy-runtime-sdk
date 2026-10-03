package supervisor

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"sync"

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy-runtime-sdk/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type environmentEntry struct {
	Present bool
	Value   []byte
}

type environmentReport struct {
	Plugin map[string]environmentEntry
	Child  map[string]environmentEntry
}

var compatibilityKeys = []string{
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"ALL_PROXY",
	"NO_PROXY",
	"SSL_CERT_FILE",
	"SSL_CERT_DIR",
	"HOME",
	"XDG_CONFIG_HOME",
	"XDG_DATA_HOME",
	"XDG_CACHE_HOME",
	"DOCKER_HOST",
	"DOCKER_CONTEXT",
	"DOCKER_CONFIG",
	"TMPDIR",
	"TMP",
	"TEMP",
	"PATH",
	"DEVSY_ENV_PROBE_RUNTIME_CONFIG",
	"DEVSY_ENV_PROBE_SECRET",
}

func environmentSnapshot() map[string]environmentEntry {
	keys := append([]string{}, compatibilityKeys...)
	keys = append(
		keys,
		sdkplugin.Handshake().MagicCookieKey,
		"PLUGIN_PROTOCOL_VERSIONS",
		"PLUGIN_CLIENT_CERT",
		"PLUGIN_MULTIPLEX_GRPC",
		"PLUGIN_MIN_PORT",
		"PLUGIN_MAX_PORT",
		"PLUGIN_UNIX_SOCKET_DIR",
	)
	values := make(map[string]environmentEntry, len(keys))
	for _, key := range keys {
		value, present := os.LookupEnv(key)
		values[key] = environmentEntry{Present: present, Value: []byte(value)}
	}
	return values
}

func environmentChildMain() {
	if err := json.NewEncoder(os.Stdout).Encode(environmentSnapshot()); err != nil {
		panic(err)
	}
}

type environmentFixture struct {
	runtimev1.UnimplementedRuntimeDriverServer
}

func serveEnvironment() {
	if _, err := io.WriteString(os.Stderr, "environment fixture serving\n"); err != nil {
		panic(err)
	}
	server.Serve(&environmentFixture{})
}

func (*environmentFixture) Exec(
	s grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage],
) error {
	if err := environmentStart(s); err != nil {
		return err
	}
	report, err := childEnvironment()
	if err != nil {
		return status.Error(codes.Internal, "environment child failed")
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if len(data) > runtimev1.ChunkSize {
		return status.Error(codes.Internal, "fixture report exceeds frame bound")
	}
	if err := s.Send(&runtimev1.ExecServerMessage{Payload: &runtimev1.ExecServerMessage_Stdout{
		Stdout: &runtimev1.OutputChunk{Data: data},
	}}); err != nil {
		return err
	}
	return s.Send(
		&runtimev1.ExecServerMessage{
			Payload: &runtimev1.ExecServerMessage_Exit{Exit: &runtimev1.ExecExit{}},
		},
	)
}

func environmentStart(
	s grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage],
) error {
	first, err := s.Recv()
	if err != nil {
		return err
	}
	if first.GetStart() == nil {
		return status.Error(codes.InvalidArgument, "expected Start")
	}
	closed, err := s.Recv()
	if err != nil {
		return err
	}
	if closed.GetCloseStdin() == nil {
		return status.Error(codes.InvalidArgument, "expected CloseStdin")
	}
	if _, err := s.Recv(); err != io.EOF {
		return status.Error(codes.InvalidArgument, "expected send-side EOF")
	}
	return nil
}

func childEnvironment() (environmentReport, error) {
	report := environmentReport{Plugin: environmentSnapshot()}
	binary, err := os.Executable()
	if err != nil {
		return report, err
	}
	// #nosec G204 -- Absolute self-executable test fixture; no PATH resolution.
	command := exec.Command(binary, "--helper-role=environment-child")
	data, err := command.Output()
	if err != nil {
		return report, err
	}
	err = json.Unmarshal(data, &report.Child)
	return report, err
}

type environmentDiagnostics struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (d *environmentDiagnostics) Write(data []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.data.Write(data)
}

func (d *environmentDiagnostics) snapshot() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return bytes.Clone(d.data.Bytes())
}
