package temporary_ledger_leak_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stallsettle/internal/domain"
	"stallsettle/internal/fileledger"
)

func TestFailedSaveRemovesTemporaryLedger(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "occupied")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	ledger, err := fileledger.New(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Save(context.Background(), domain.NewEmptySnapshot()); err == nil {
		t.Fatal("目标为目录时保存意外成功")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stallsettle-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("保存失败后遗留临时账本 %q", entry.Name())
		}
	}
}
