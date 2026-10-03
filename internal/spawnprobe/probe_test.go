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

var executable string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "devsy-spawn-probe-")
	if err != nil {
		panic(err)
	}
	executable = filepath.Join(dir, "runtime λ fixture")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	// No race instrumentation in the measured executable: its shutdown delay
	// would dominate the process lifetime measurement.
	// #nosec G204 -- Fixed fixture package, locally-created output directory.
	command := exec.Command("go", "build", "-o", executable, "../../cmd/devsy-fake-runtime")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Binary: executable, Args: []string{"--state-dir", t.TempDir()},
		Samples: 2, Timeout: 5 * time.Second, WorkspaceID: "absent",
	}
}

func TestRealPluginMeasurements(t *testing.T) {
	report, err := Run(context.Background(), testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if report.GOOS != runtime.GOOS || report.GOARCH != runtime.GOARCH ||
		len(report.BinarySHA256) != 64 {
		t.Fatalf("missing reproducibility metadata: %v", report)
	}
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
	config := testConfig(t)
	config.Args = append(config.Args, "--mode", "delay-handshake", "--handshake-delay", "1m")
	config.Timeout = 100 * time.Millisecond
	if _, err := Run(context.Background(), config); err == nil {
		t.Fatal("delayed startup accepted")
	}
}
