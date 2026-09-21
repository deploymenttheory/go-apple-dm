package appartifact

import (
	"errors"
	"fmt"
	"io"

	"github.com/deploymenttheory/go-apfs-v2/pkg/apfs"
	"github.com/deploymenttheory/go-apfs-v2/pkg/disk"
	"github.com/deploymenttheory/go-apfs-v2/pkg/hfsplus"
)

// dmg reads a disk image without mounting it and inspects discovered application content
// within configured limits.
func (i *inspector) dmg(filename, location string, depth int) error {
	// #nosec G115 -- Inspect validates positive byte limits at or below one TiB.
	r, err := disk.OpenDMGWithLimits(filename, disk.DMGLimits{ImageBytes: uint64(i.opts.MaxExpandedBytes), ChunkBytes: uint64(min(i.opts.MaxBytes, 64<<20))})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	defer func() { _ = r.Close() }()
	if r.Size() < 0 || r.Size() > i.opts.MaxExpandedBytes {
		return ErrLimit
	}
	var magic [4]byte
	if _, err := r.ReadAt(magic[:], 32); err != nil {
		return err
	}
	if string(magic[:]) == "NXSB" {
		container, err := apfs.Open(r, nil)
		if err != nil {
			return err
		}
		defer func() { _ = container.Close() }()
		volumes, err := container.Volumes()
		if err != nil {
			return err
		}
		if len(volumes) > i.opts.MaxEntries {
			return ErrLimit
		}
		for index, volume := range volumes {
			if err := i.walk(volume, fmt.Sprintf("%s!volume-%d", location, index), depth); err != nil {
				return err
			}
		}
		return nil
	}
	if _, err := r.ReadAt(magic[:2], 1024); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if string(magic[:2]) != "H+" && string(magic[:2]) != "HX" {
		return ErrUnsupported
	}
	volume, err := hfsplus.New(r)
	if err != nil {
		return err
	}
	return i.walk(volume, location+"!volume-0", depth)
}
