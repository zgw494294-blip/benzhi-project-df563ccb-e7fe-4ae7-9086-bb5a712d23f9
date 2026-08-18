package fileledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"stallsettle/internal/domain"
)

const DefaultMaxBytes int64 = 4 << 20

type FileLedger struct {
	path     string
	maxBytes int64
}

func New(path string) (*FileLedger, error) {
	if path == "" {
		return nil, errors.New("ledger path is required")
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || filepath.Base(cleaned) == "." {
		return nil, errors.New("ledger path must name a file")
	}
	return &FileLedger{path: cleaned, maxBytes: DefaultMaxBytes}, nil
}

func NewWithLimit(path string, maxBytes int64) (*FileLedger, error) {
	if maxBytes <= 0 {
		return nil, errors.New("ledger size limit must be positive")
	}
	ledger, err := New(path)
	if err != nil {
		return nil, err
	}
	ledger.maxBytes = maxBytes
	return ledger, nil
}

func (l *FileLedger) Load(ctx context.Context) (domain.LedgerSnapshot, error) {
	if err := contextError(ctx); err != nil {
		return domain.LedgerSnapshot{}, err
	}
	info, err := os.Lstat(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return domain.NewEmptySnapshot(), nil
	}
	if err != nil {
		return domain.LedgerSnapshot{}, fmt.Errorf("stat ledger: %w", err)
	}
	if !info.Mode().IsRegular() {
		return domain.LedgerSnapshot{}, errors.New("ledger path is not a regular file")
	}
	if info.Size() > l.maxBytes {
		return domain.LedgerSnapshot{}, fmt.Errorf("ledger exceeds %d bytes", l.maxBytes)
	}
	file, err := os.Open(l.path)
	if err != nil {
		return domain.LedgerSnapshot{}, fmt.Errorf("open ledger: %w", err)
	}
	var snapshot domain.LedgerSnapshot
	decodeErr := decode(file, l.maxBytes, &snapshot)
	closeErr := file.Close()
	if decodeErr != nil {
		return domain.LedgerSnapshot{}, fmt.Errorf("decode ledger: %w", decodeErr)
	}
	if closeErr != nil {
		return domain.LedgerSnapshot{}, fmt.Errorf("close ledger: %w", closeErr)
	}
	if err := snapshot.Validate(); err != nil {
		return domain.LedgerSnapshot{}, fmt.Errorf("validate ledger: %w", err)
	}
	return snapshot, nil
}

func (l *FileLedger) Save(ctx context.Context, snapshot domain.LedgerSnapshot) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("validate candidate: %w", err)
	}
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode candidate: %w", err)
	}
	if int64(len(payload)) > l.maxBytes {
		return fmt.Errorf("candidate exceeds %d bytes", l.maxBytes)
	}
	directory := filepath.Dir(l.path)
	if directory == "" {
		directory = "."
	}
	if err := ensureDirectory(ctx, directory); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".stallsettle-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary ledger: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := writeAll(temporary, payload); err != nil {
		return fmt.Errorf("write temporary ledger: %w", err)
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary ledger: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary ledger: %w", err)
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, l.path); err != nil {
		return fmt.Errorf("replace ledger: %w", err)
	}
	return syncDirectory(directory)
}

func decode(reader io.Reader, maxBytes int64, target *domain.LedgerSnapshot) error {
	limited := &io.LimitedReader{R: reader, N: maxBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON data")
		}
		return err
	}
	if limited.N <= 0 {
		return fmt.Errorf("ledger exceeds %d bytes", maxBytes)
	}
	return nil
}

func ensureDirectory(ctx context.Context, directory string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0750); err != nil {
			return fmt.Errorf("create ledger directory: %w", err)
		}
		info, err = os.Stat(directory)
	}
	if err != nil {
		return fmt.Errorf("stat ledger directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("ledger parent path is not a directory")
	}
	return contextError(ctx)
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}

func syncDirectory(directory string) error {
	directoryFile, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open ledger directory: %w", err)
	}
	syncErr := directoryFile.Sync()
	closeErr := directoryFile.Close()
	if syncErr != nil {
		return fmt.Errorf("sync ledger directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close ledger directory: %w", closeErr)
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
