// Package storage moves files to a worker and back.
//
// Shared: the worker sees the same shared storage, only paths are passed
// (remapped when the worker mounts it elsewhere). Upload: fallback without
// shared storage — inputs go to the worker's scratch dir, outputs come back.
package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Storage interface {
	// Input makes a local file available to the worker and returns its worker path.
	Input(ctx context.Context, local string) (string, error)
	// OutputDir returns where the worker writes outputs that belong in localDir.
	OutputDir(ctx context.Context, localDir string) (string, error)
	// Fetch makes sure the worker output remote ends up at the local path.
	Fetch(ctx context.Context, remote, local string) error
}

// Transport is the worker file API (POST/GET /v1/files).
type Transport interface {
	Upload(ctx context.Context, local string) (path, dir string, err error)
	Download(ctx context.Context, remote string, w io.Writer) error
}

// New picks the implementation from the worker config.
func New(kind, localRoot, remoteRoot string, t Transport) (Storage, error) {
	switch kind {
	case "", "shared":
		if remoteRoot == "" {
			remoteRoot = localRoot
		}
		return Shared{LocalRoot: filepath.Clean(localRoot), RemoteRoot: filepath.Clean(remoteRoot)}, nil
	case "upload":
		return &Upload{T: t}, nil
	}
	return nil, fmt.Errorf("neznámý storage %q (shared|upload)", kind)
}

type Shared struct {
	LocalRoot, RemoteRoot string
}

func (s Shared) Input(_ context.Context, local string) (string, error) {
	return s.toRemote(local)
}

func (s Shared) OutputDir(_ context.Context, localDir string) (string, error) {
	return s.toRemote(localDir)
}

func (s Shared) Fetch(_ context.Context, remote, local string) error {
	got, err := rebase(remote, s.RemoteRoot, s.LocalRoot)
	if err != nil {
		return err
	}
	if _, err := os.Stat(got); err != nil {
		return fmt.Errorf("výstup workeru není na sdíleném úložišti: %w", err)
	}
	if got != filepath.Clean(local) {
		return os.Rename(got, local)
	}
	return nil
}

func (s Shared) toRemote(local string) (string, error) {
	abs, err := filepath.Abs(local)
	if err != nil {
		return "", err
	}
	return rebase(abs, s.LocalRoot, s.RemoteRoot)
}

func rebase(p, from, to string) (string, error) {
	rel, err := filepath.Rel(from, filepath.Clean(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%s není pod sdíleným kořenem %s", p, from)
	}
	return filepath.Join(to, rel), nil
}

// Upload remembers the scratch dir of the last uploaded input; outputs go
// to its out/ subdir. One instance per job.
type Upload struct {
	T   Transport
	dir string
}

func (u *Upload) Input(ctx context.Context, local string) (string, error) {
	path, dir, err := u.T.Upload(ctx, local)
	if err != nil {
		return "", fmt.Errorf("upload %s: %w", filepath.Base(local), err)
	}
	u.dir = dir
	return path, nil
}

func (u *Upload) OutputDir(context.Context, string) (string, error) {
	if u.dir == "" {
		return "", fmt.Errorf("upload storage: OutputDir před Input")
	}
	return u.dir + "/out", nil
}

func (u *Upload) Fetch(ctx context.Context, remote, local string) error {
	tmp := filepath.Join(filepath.Dir(local), "."+filepath.Base(local)+".tmp")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := u.T.Download(ctx, remote, f); err != nil {
		f.Close()
		return fmt.Errorf("download %s: %w", remote, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, local)
}
