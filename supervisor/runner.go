// Package supervisor supplies explicit, host-leased runtime process ownership.
package supervisor

import (
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin/runner"
)

// Options selects already verified executables. Env overrides the environment
// selected by go-plugin. Restricting runtime inheritance requires the client
// to set SkipHostEnv; supplying a few Env entries alone does not restrict it.
// Client-assigned transport metadata takes precedence over Env overrides.
type Options struct {
	SupervisorBinary string
	SupervisorArgs   []string
	RuntimeBinary    string
	Args             []string
	Env              []string
	Directory        string
}

// Runner returns a go-plugin RunnerFunc. Configure RunnerFunc instead of Cmd;
// the go-plugin client explicitly owns the supervisor until Client.Kill returns.
// SupervisorBinary must implement Main's dedicated helper entry point.
func Runner(options Options) func(hclog.Logger, *exec.Cmd, string) (runner.Runner, error) {
	options.Args, options.Env = slices.Clone(options.Args), slices.Clone(options.Env)
	options.SupervisorArgs = slices.Clone(options.SupervisorArgs)
	return func(_ hclog.Logger, spec *exec.Cmd, socketDir string) (runner.Runner, error) {
		if err := validateOptions(options); err != nil {
			_ = os.RemoveAll(socketDir)
			return nil, err
		}
		cfg := configuration{
			Version:   1,
			Binary:    options.RuntimeBinary,
			Args:      options.Args,
			Env:       runtimeEnvironment(spec.Environ(), options.Env),
			Directory: options.Directory,
			SocketDir: socketDir,
		}
		owned, err := newRunner(options.SupervisorBinary, options.SupervisorArgs, spec.Stdin, cfg)
		if err != nil {
			return nil, errors.Join(err, os.RemoveAll(socketDir))
		}
		return owned, nil
	}
}

func validateOptions(options Options) error {
	for _, path := range []string{options.SupervisorBinary, options.RuntimeBinary} {
		if !filepath.IsAbs(path) {
			return errors.New("supervisor and runtime binaries must be absolute paths")
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("supervisor and runtime binaries must be regular files")
		}
	}
	if options.Directory != "" && !filepath.IsAbs(options.Directory) {
		return errors.New("runtime directory must be absolute")
	}
	return nil
}

type ownedRunner struct {
	cmd                      *exec.Cmd
	config                   configuration
	leaseRead, leaseWrite    *os.File
	configRead, configWrite  *os.File
	stdout, stderr           io.ReadCloser
	stdoutWrite, stderrWrite *os.File
	done                     chan struct{}
	waitErr                  error
	stop                     sync.Once
}

func newRunner(
	binary string,
	args []string,
	stdin io.Reader,
	cfg configuration,
) (*ownedRunner, error) {
	r := &ownedRunner{config: cfg, done: make(chan struct{})}
	var err error
	r.leaseRead, r.leaseWrite, err = os.Pipe()
	if err != nil {
		return nil, err
	}
	r.configRead, r.configWrite, err = os.Pipe()
	if err != nil {
		r.closeFiles()
		return nil, err
	}
	// #nosec G204 -- Explicitly trusted, absolute supervisor executable; no shell or PATH resolution.
	r.cmd = exec.Command(binary, args...)
	r.cmd.Stdin = stdin
	if err := configurePipes(r.cmd, r.leaseRead, r.configRead); err != nil {
		r.closeFiles()
		return nil, err
	}
	if err := r.outputPipes(); err != nil {
		r.closeFiles()
		return nil, err
	}
	return r, nil
}

func (r *ownedRunner) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		r.closeFiles()
		return err
	}
	if err := r.cmd.Start(); err != nil {
		r.closeFiles()
		return err
	}
	r.closeChildFiles()
	go func() { r.waitErr = r.cmd.Wait(); r.closeLease(); close(r.done) }()
	encoded := make(chan error, 1)
	go func() {
		encoded <- gob.NewEncoder(r.configWrite).Encode(r.config)
		_ = r.configWrite.Close()
	}()
	select {
	case err := <-encoded:
		if err == nil {
			return nil
		}
		r.closeLease()
		return errors.Join(err, r.Wait(context.Background()))
	case <-ctx.Done():
		r.closeLease()
		_ = r.configWrite.Close()
		return errors.Join(ctx.Err(), r.Wait(context.Background()))
	}
}

func (r *ownedRunner) Kill(ctx context.Context) error {
	r.closeLease()
	return r.Wait(ctx)
}

func (r *ownedRunner) Wait(ctx context.Context) error {
	select {
	case <-r.done:
		return r.waitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *ownedRunner) ID() string {
	if r.cmd.Process == nil {
		return ""
	}
	return strconv.Itoa(r.cmd.Process.Pid)
}

func (r *ownedRunner) Name() string          { return r.config.Binary }
func (r *ownedRunner) Stdout() io.ReadCloser { return r.stdout }
func (r *ownedRunner) Stderr() io.ReadCloser { return r.stderr }
func (r *ownedRunner) Diagnose(context.Context) string {
	return "runtime supervisor failed; inspect forwarded stderr"
}

func (*ownedRunner) PluginToHost(
	network, address string,
) (translatedNetwork, translatedAddress string, err error) {
	return network, address, nil
}

func (*ownedRunner) HostToPlugin(
	network, address string,
) (translatedNetwork, translatedAddress string, err error) {
	return network, address, nil
}

func (r *ownedRunner) closeFiles() {
	for _, file := range []*os.File{r.leaseRead, r.leaseWrite, r.configRead, r.configWrite, r.stdoutWrite, r.stderrWrite} {
		if file != nil {
			_ = file.Close()
		}
	}
	if r.stdout != nil {
		_ = r.stdout.Close()
	}
	if r.stderr != nil {
		_ = r.stderr.Close()
	}
	_ = os.RemoveAll(r.config.SocketDir)
}

func (r *ownedRunner) closeLease() { r.stop.Do(func() { _ = r.leaseWrite.Close() }) }

// configuration travels only through an inherited pipe, never process arguments or disk.
type configuration struct {
	Version              int
	Binary               string
	Args, Env            []string
	Directory, SocketDir string
}

func (c configuration) validate() error {
	if c.Version != 1 {
		return fmt.Errorf("runtime supervisor protocol %d is unsupported; expected 1", c.Version)
	}
	if !filepath.IsAbs(c.Binary) {
		return errors.New("runtime executable must be absolute")
	}
	if !filepath.IsAbs(c.SocketDir) {
		return errors.New("supervisor socket directory must be absolute")
	}
	return nil
}

func (r *ownedRunner) outputPipes() error {
	output, writeOutput, err := os.Pipe()
	if err != nil {
		return err
	}
	r.stdout, r.stdoutWrite = &drainingPipe{File: output}, writeOutput
	diagnostic, writeDiagnostic, err := os.Pipe()
	if err != nil {
		return err
	}
	r.stderr, r.stderrWrite = &drainingPipe{File: diagnostic}, writeDiagnostic
	r.cmd.Stdout, r.cmd.Stderr = writeOutput, writeDiagnostic
	return nil
}

func (r *ownedRunner) closeChildFiles() {
	for _, file := range []*os.File{r.leaseRead, r.configRead, r.stdoutWrite, r.stderrWrite} {
		_ = file.Close()
	}
}

type drainingPipe struct{ *os.File }

func (p *drainingPipe) Read(data []byte) (int, error) {
	n, err := p.File.Read(data)
	if err != nil {
		_ = p.Close()
	}
	return n, err
}

func runtimeEnvironment(base, overrides []string) []string {
	env := append(slices.Clone(base), overrides...)
	cookie := sdkplugin.Handshake().MagicCookieKey
	for _, value := range base {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case cookie,
			"PLUGIN_MIN_PORT",
			"PLUGIN_MAX_PORT",
			"PLUGIN_PROTOCOL_VERSIONS",
			"PLUGIN_CLIENT_CERT",
			"PLUGIN_MULTIPLEX_GRPC",
			"PLUGIN_UNIX_SOCKET_GROUP",
			"PLUGIN_UNIX_SOCKET_DIR":
			// Client-assigned transport metadata must survive inherited or provider environment overrides.
			env = append(env, value)
		}
	}
	return env
}
