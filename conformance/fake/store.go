package fake

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/protobuf/encoding/protojson"
)

func (d *Driver) statePath(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(d.config.StateDir, hex.EncodeToString(sum[:])+".json")
}

func (d *Driver) load(id string) (*runtimev1.ContainerDetails, error) {
	data, err := os.ReadFile(d.statePath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var container runtimev1.ContainerDetails
	if err := protojson.Unmarshal(data, &container); err != nil {
		return nil, err
	}
	if container.GetId() != id ||
		(container.GetState().GetStatus() != running && container.GetState().GetStatus() != stopped) {
		return nil, errors.New("invalid fake runtime state record")
	}
	return &container, nil
}

func (d *Driver) save(container *runtimev1.ContainerDetails) error {
	data, err := protojson.Marshal(container)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(d.config.StateDir, ".state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Replace only a complete record, so a killed fixture never leaves partial JSON.
	return os.Rename(file.Name(), d.statePath(container.GetId()))
}
