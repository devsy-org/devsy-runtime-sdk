package spawnprobe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy-runtime-sdk/supervisor"
	"github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"
)

// Config supplies an explicitly trusted executable and bounded measurement count.
type Config struct {
	SupervisorBinary string
	Binary           string
	Args             []string
	Samples          int
	Timeout          time.Duration
	WorkspaceID      string
	Revision         string
	Diagnostics      io.Writer
}

// Run measures one first launch and Samples warm launches for Info and Find.
// Building, hashing, and report encoding occur outside the measured operation.
func Run(ctx context.Context, config Config) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.Diagnostics == nil {
		config.Diagnostics = os.Stderr
	}
	checksum, err := validate(config)
	if err != nil {
		return nil, err
	}
	info, err := measure(ctx, config, "Info")
	if err != nil {
		return nil, err
	}
	find, err := measure(ctx, config, "Find")
	if err != nil {
		return nil, err
	}
	return &Report{
		SchemaVersion: 1, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		GoVersion: runtime.Version(), CPUs: runtime.NumCPU(), Revision: config.Revision,
		LaunchMode: launchMode(config), BinarySHA256: checksum.runtime,
		SupervisorSHA256: checksum.supervisor, RecordedAt: time.Now().UTC(), Info: info, Find: find,
	}, nil
}

type binaryHashes struct {
	runtime    string
	supervisor string
}

func launchMode(config Config) string {
	if config.SupervisorBinary != "" {
		return "supervised"
	}
	return "direct"
}

func validate(config Config) (binaryHashes, error) {
	if !filepath.IsAbs(config.Binary) {
		return binaryHashes{}, errors.New("plugin binary must be an absolute path")
	}
	if config.Samples < 1 || config.Samples > 10000 {
		return binaryHashes{}, errors.New("samples must be between 1 and 10000")
	}
	if config.Timeout <= 0 {
		return binaryHashes{}, errors.New("operation timeout must be positive")
	}
	if config.WorkspaceID == "" {
		return binaryHashes{}, errors.New("workspace ID is required for Find")
	}
	return artifactHashes(config)
}

func artifactHashes(config Config) (binaryHashes, error) {
	hashes := binaryHashes{}
	var err error
	hashes.runtime, err = checksum(config.Binary)
	if err != nil {
		return hashes, err
	}
	if config.SupervisorBinary != "" {
		if !filepath.IsAbs(config.SupervisorBinary) {
			return hashes, errors.New("supervisor binary must be an absolute path")
		}
		hashes.supervisor, err = checksum(config.SupervisorBinary)
		if err != nil {
			return hashes, fmt.Errorf("supervisor: %w", err)
		}
	}
	return hashes, nil
}

func checksum(binary string) (string, error) {
	metadata, err := os.Stat(binary)
	if err != nil {
		return "", err
	}
	if !metadata.Mode().IsRegular() {
		return "", errors.New("plugin binary must be a regular file")
	}
	// #nosec G304 -- Caller explicitly supplies a trusted absolute plugin executable.
	file, err := os.Open(binary)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	metadata, err = file.Stat()
	if err != nil {
		return "", err
	}
	if !metadata.Mode().IsRegular() {
		return "", errors.New("plugin binary must be a regular file")
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func measure(ctx context.Context, config Config, operation string) (Measurements, error) {
	first, err := measureOne(ctx, config, operation)
	if err != nil {
		return Measurements{}, fmt.Errorf("%s first launch: %w", operation, err)
	}
	samples := make([]Sample, 0, config.Samples)
	for index := range config.Samples {
		sample, err := measureOne(ctx, config, operation)
		if err != nil {
			return Measurements{}, fmt.Errorf("%s sample %d: %w", operation, index+1, err)
		}
		samples = append(samples, sample)
	}
	return Measurements{
		Operation: operation,
		First:     first,
		Warm:      samples,
		Total:     summarize(samples),
	}, nil
}

func measureOne(
	parent context.Context,
	config Config,
	operation string,
) (sample Sample, resultErr error) {
	if err := parent.Err(); err != nil {
		return sample, err
	}
	ctx, cancel := context.WithTimeout(parent, config.Timeout)
	defer cancel()
	client := newClient(ctx, config)
	started := time.Now()
	defer func() {
		cleanupErr := reap(client, &sample, started)
		if resultErr == nil {
			resultErr = cleanupErr
		}
	}()

	rpc, err := client.Client()
	sample.StartupNS = time.Since(started).Nanoseconds()
	if err != nil {
		return sample, err
	}
	driver, elapsed, err := dispense(rpc)
	sample.DispenseNS = elapsed
	if err != nil {
		return sample, err
	}
	rpcStarted := time.Now()
	resultErr = call(ctx, driver, operation, config.WorkspaceID)
	sample.RPCNS = time.Since(rpcStarted).Nanoseconds()
	return sample, resultErr
}

func dispense(rpc hplugin.ClientProtocol) (runtimev1.RuntimeDriverClient, int64, error) {
	started := time.Now()
	raw, err := rpc.Dispense(sdkplugin.Name)
	elapsed := time.Since(started).Nanoseconds()
	if err != nil {
		return nil, elapsed, err
	}
	driver, ok := raw.(runtimev1.RuntimeDriverClient)
	if !ok {
		return nil, elapsed, errors.New("plugin did not return a runtime client")
	}
	return driver, elapsed, nil
}

func call(
	ctx context.Context,
	driver runtimev1.RuntimeDriverClient,
	operation, workspaceID string,
) error {
	if operation == "Info" {
		info, err := driver.Info(ctx, &runtimev1.InfoRequest{})
		if err != nil {
			return err
		}
		return runtimev1.ValidateInfo(info)
	}
	found, err := driver.Find(ctx, &runtimev1.FindRequest{WorkspaceId: workspaceID})
	if err != nil {
		return err
	}
	if found.GetFound() {
		return errors.New("find probe requires an absent workspace; choose an unused workspace ID")
	}
	return nil
}

func newClient(ctx context.Context, config Config) *hplugin.Client {
	clientConfig := &hplugin.ClientConfig{
		HandshakeConfig: sdkplugin.Handshake(),
		VersionedPlugins: map[int]hplugin.PluginSet{
			sdkplugin.ProtocolVersion: sdkplugin.ClientPlugins(),
		},
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
		StartTimeout:     config.Timeout,
		Logger:           hclog.NewNullLogger(),
		Stderr:           config.Diagnostics,
		SyncStderr:       config.Diagnostics,
	}
	if config.SupervisorBinary == "" {
		// #nosec G204 -- Explicitly trusted absolute executable, validated before measurement.
		clientConfig.Cmd = exec.CommandContext(ctx, config.Binary, config.Args...)
	} else {
		clientConfig.RunnerFunc = supervisor.Runner(supervisor.Options{
			SupervisorBinary: config.SupervisorBinary,
			RuntimeBinary:    config.Binary,
			Args:             config.Args,
		})
	}
	return hplugin.NewClient(clientConfig)
}

func reap(client *hplugin.Client, sample *Sample, started time.Time) error {
	reapStarted := time.Now()
	pid, pidErr := strconv.Atoi(client.ID())
	sample.PID = pid
	client.Kill()
	sample.Reaped = client.Exited()
	sample.ReapNS = time.Since(reapStarted).Nanoseconds()
	sample.TotalNS = time.Since(started).Nanoseconds()
	if pidErr != nil || sample.PID <= 0 {
		return errors.New("runner did not report a process ID")
	}
	if !sample.Reaped {
		return errors.New("runner was not reaped")
	}
	return nil
}
