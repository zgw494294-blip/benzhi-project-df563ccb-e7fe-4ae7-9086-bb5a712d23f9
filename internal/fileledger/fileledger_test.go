package fileledger

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"stallsettle/internal/domain"
)

func TestFileLedgerRoundTripAndAtomicReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "nested", "ledger.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	ledger, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := domain.NewEmptySnapshot()
	consignment, err := domain.NewConsignment("csn-file", "摊主", 100, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Consignments[consignment.ID] = consignment
	snapshot.SavedAt = time.Now().UTC()
	if err := ledger.Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := ledger.Load(context.Background())
	if err != nil || loaded.Consignments[consignment.ID].SellerName != "摊主" {
		t.Fatalf("round trip = %#v, %v", loaded, err)
	}
	if strings.Contains(string(mustRead(t, path)), "\n\n") {
		t.Fatalf("unexpected empty record in saved ledger")
	}
}

func TestFileLedgerRejectsTrailingAndUnknownJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	if err := os.WriteFile(path, []byte(`{"format_version":1,"consignments":{},"saved_at":"0001-01-01T00:00:00Z"} {}`), 0600); err != nil {
		t.Fatal(err)
	}
	ledger, _ := New(path)
	if _, err := ledger.Load(context.Background()); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
	if err := os.WriteFile(path, []byte(`{"format_version":1,"consignments":{},"saved_at":"0001-01-01T00:00:00Z","extra":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Load(context.Background()); err == nil {
		t.Fatal("unknown JSON was accepted")
	}
}

func TestFileLedgerCanceledSaveLeavesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	ledger, _ := New(path)
	initial := domain.NewEmptySnapshot()
	if err := ledger.Save(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	original := mustRead(t, path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ledger.Save(ctx, initial); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled save error = %v", err)
	}
	if got := mustRead(t, path); got != original {
		t.Fatal("canceled save changed the ledger")
	}
}

func TestFileLedgerCreatesNestedParentAndHonorsLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "one", "two", "ledger.json")
	ledger, err := NewWithLimit(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Save(context.Background(), domain.NewEmptySnapshot()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("saved ledger info = %#v, %v", info, err)
	}
	tooSmall, err := NewWithLimit(path, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tooSmall.Load(context.Background()); err == nil {
		t.Fatal("ledger larger than configured limit was accepted")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
