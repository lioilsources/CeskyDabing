// Package storage řeší, jak se soubory dostanou k workeru a zpět.
//
// Shared: worker vidí stejné NFS (/mnt/dabing) — předávají se jen cesty,
// případně přemapované, pokud ho worker má jinde. Upload: fallback bez NFS —
// vstup se nahraje do scratch adresáře workeru a výstupy se stáhnou zpět.
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
	// Input zpřístupní lokální soubor workeru a vrátí cestu, jak ji vidí worker.
	Input(ctx context.Context, local string) (string, error)
	// OutputDir vrátí adresář, kam má worker zapsat výstupy patřící do localDir.
	OutputDir(ctx context.Context, localDir string) (string, error)
	// Fetch zajistí, že výstup workeru remote leží lokálně na cestě local.
	Fetch(ctx context.Context, remote, local string) error
}

// Transport je souborové API workeru (POST/GET /v1/files).
type Transport interface {
	Upload(ctx context.Context, local string) (path, dir string, err error)
	Download(ctx context.Context, remote string, w io.Writer) error
}

// New vybere implementaci podle configu workeru.
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

// Upload si pamatuje scratch adresář posledního nahraného vstupu; výstupy
// jdou do jeho podadresáře out/. Jedna instance = jedna úloha.
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
