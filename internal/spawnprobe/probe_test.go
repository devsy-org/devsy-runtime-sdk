package spawnprobe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var executable, supervisorExecutable string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "devsy-spawn-probe-")
	if err != nil {
		panic(err)
	}
	executable = filepath.Join(dir, "runtime λ fixture")
	supervisorExecutable = filepath.Join(dir, "supervisor λ fixture")
	if runtime.GOOS == "windows" {
		executable += ".exe"
		supervisorExecutable += ".exe"
	}
	for binary, packagePath := range map[string]string{
		executable:           "../../cmd/devsy-fake-runtime",
		supervisorExecutable: "../../cmd/devsy-runtime-supervisor",
	} {
		if err := buildFixture(binary, packagePath); err != nil {
			_ = os.RemoveAll(dir)
			os.Exit(1)
		}
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func buildFixture(binary, packagePath string) error {
	// No race instrumentation: its shutdown delay would distort lifetime measurements.
	// #nosec G204 -- Fixed fixture packages and locally-created output directory.
	command := exec.Command("go", "build", "-o", binary, packagePath)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Binary: executable, Args: []string{"--state-dir", t.TempDir()},
		Samples: 2, Timeout: 5 * time.Second, WorkspaceID: "absent",
	}
}

func TestRealPluginMeasurements(t *testing.T) {
	for _, owned := range []bool{false, true} {
		config := testConfig(t)
		if owned {
			config.SupervisorBinary = supervisorExecutable
		}
		t.Run(launchMode(config), func(t *testing.T) { checkRealMeasurements(t, config) })
	}
}

func checkRealMeasurements(t *testing.T, config Config) {
	t.Helper()
	report, err := Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if report.GOOS != runtime.GOOS || report.GOARCH != runtime.GOARCH ||
		len(report.BinarySHA256) != 64 {
		t.Fatalf("missing reproducibility metadata: %v", report)
	}
	assertLaunchMetadata(t, config, report)
	if report.ColdCacheMeasured {
		t.Fatal("warm file cache was labeled cold")
	}
	for _, measurement := range []Measurements{report.Info, report.Find} {
		assertMeasurement(t, measurement)
	}
}

func assertMeasurement(t *testing.T, measurement Measurements) {
	t.Helper()
	if len(measurement.Warm) != 2 {
		t.Fatal("incorrect warm sample count")
	}
	for _, sample := range append([]Sample{measurement.First}, measurement.Warm...) {
		if !sample.Reaped || sample.PID <= 0 {
			t.Fatal("process was not launched and reaped")
		}
		if sample.TotalNS < sample.StartupNS+sample.DispenseNS+sample.RPCNS+sample.ReapNS {
			t.Fatal("complete duration omits part of the operation")
		}
	}
	if measurement.Total != summarize(measurement.Warm) {
		t.Fatal("first launch included in warm percentiles")
	}
}

func TestInvalidConfiguration(t *testing.T) {
	cases := []func(*Config){
		func(c *Config) { c.Binary = "runtime-from-PATH" },
		func(c *Config) { c.Binary = t.TempDir() },
		func(c *Config) { c.SupervisorBinary = "supervisor-from-PATH" },
		func(c *Config) { c.SupervisorBinary = t.TempDir() },
		func(c *Config) { c.SupervisorBinary = filepath.Join(t.TempDir(), "missing") },
		func(c *Config) { c.Samples = 0 },
		func(c *Config) { c.Samples = 10001 },
		func(c *Config) { c.Timeout = 0 },
		func(c *Config) { c.WorkspaceID = "" },
	}
	for _, mutate := range cases {
		config := testConfig(t)
		mutate(&config)
		if _, err := Run(context.Background(), config); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestCancellationAndMalformedInfo(t *testing.T) {
	config := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, config); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	config.Args = append(config.Args, "--mode", "malformed-info")
	if _, err := Run(context.Background(), config); err == nil {
		t.Fatal("invalid Info accepted")
	}
}

func TestStartupTimeout(t *testing.T) {
	for _, owned := range []bool{false, true} {
		config := testConfig(t)
		if owned {
			config.SupervisorBinary = supervisorExecutable
		}
		t.Run(launchMode(config), func(t *testing.T) { checkStartupTimeout(t, config) })
	}
}

func checkStartupTimeout(t *testing.T, config Config) {
	t.Helper()
	config.Args = append(config.Args, "--mode", "delay-handshake", "--handshake-delay", "1m")
	config.Timeout = 100 * time.Millisecond
	if _, err := Run(context.Background(), config); err == nil {
		t.Fatal("delayed startup accepted")
	}
}

func assertLaunchMetadata(t *testing.T, config Config, report *Report) {
	t.Helper()
	assertArtifactHash(t, config.Binary, report.BinarySHA256)
	if config.SupervisorBinary != "" {
		assertArtifactHash(t, config.SupervisorBinary, report.SupervisorSHA256)
	}
	if report.LaunchMode != launchMode(config) ||
		(report.SupervisorSHA256 != "") != (config.SupervisorBinary != "") {
		t.Fatal("launch metadata does not identify the measurement mode")
	}
	if config.SupervisorBinary != "" && len(report.SupervisorSHA256) != 64 {
		t.Fatal("supervisor checksum missing")
	}
}

func assertArtifactHash(t *testing.T, binary, got string) {
	t.Helper()
	expected, err := checksum(binary)
	if err != nil {
		t.Fatal(err)
	}
	if got != expected {
		t.Fatal("report fingerprint does not match measured executable")
	}
}
