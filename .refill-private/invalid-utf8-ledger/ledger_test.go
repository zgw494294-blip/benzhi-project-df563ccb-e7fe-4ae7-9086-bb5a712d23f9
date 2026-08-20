package invalid_utf8_ledger_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"stallsettle/internal/fileledger"
)

func TestLedgerRejectsInvalidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	payload := []byte(`{"format_version":1,"consignments":{"csn-invalid-utf8":{"id":"csn-invalid-utf8","seller_name":"PLACEHOLDER","commission_bps":0,"state":"draft","version":1,"items":[],"sales":[],"created_at":"2026-08-18T10:00:00Z"}},"saved_at":"2026-08-18T10:00:00Z"}`)
	marker := []byte("PLACEHOLDER")
	markerAt := -1
	for i := 0; i+len(marker) <= len(payload); i++ {
		if string(payload[i:i+len(marker)]) == string(marker) {
			markerAt = i
			break
		}
	}
	if markerAt < 0 {
		t.Fatal("测试账本缺少替换标记")
	}
	payload = append(payload[:markerAt], append([]byte{0xff}, payload[markerAt+len(marker):]...)...)
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	ledger, err := fileledger.New(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ledger.Load(context.Background())
	if err == nil {
		t.Fatalf("包含无效 UTF-8 的 JSON 账本仍被加载，摊主名被替换为 %q", loaded.Consignments["csn-invalid-utf8"].SellerName)
	}
}
