package domain

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestParsePriceCentsKeepsDecimalPrecision(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"12", 1200},
		{"12.8", 1280},
		{"12.80", 1280},
		{"0.01", 1},
	}
	for _, test := range tests {
		got, err := ParsePriceCents(test.input)
		if err != nil || got != test.want {
			t.Fatalf("ParsePriceCents(%q) = %d, %v; want %d", test.input, got, err, test.want)
		}
	}
	for _, input := range []string{"-1", "1.234", "1e2", "12,00", "0", "100000000000.00"} {
		if _, err := ParsePriceCents(input); !errors.Is(err, ErrValidation) {
			t.Fatalf("ParsePriceCents(%q) error = %v", input, err)
		}
	}
}

func TestConsignmentLifecycleBuildsReceipt(t *testing.T) {
	now := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	consignment, err := NewConsignment("csn-1", "南风陶坊", 1000, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := consignment.AddItem(" a-01 ", "手工陶杯", 5, 1280); err != nil {
		t.Fatal(err)
	}
	if consignment.Items[0].SKU != "A-01" || consignment.Items[0].DisplaySKU != "a-01" {
		t.Fatalf("unexpected sku normalization: %#v", consignment.Items[0])
	}
	if err := consignment.Activate(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	event, err := consignment.RecordSale("sale-1", "A-01", 2, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if event.GrossCents != 2560 || consignment.Items[0].SoldQuantity != 2 {
		t.Fatalf("unexpected sale: %#v", event)
	}
	receipt, err := consignment.BuildReceipt(now.Add(3 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.GrossCents != 2560 || receipt.CommissionCents != 256 || receipt.SellerPayoutCents != 2304 || receipt.Lines[0].ReturnedQuantity != 3 {
		t.Fatalf("unexpected receipt: %#v", receipt)
	}
	if err := consignment.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := consignment.RecordSale("sale-2", "A-01", 1, now); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("sale after settlement error = %v", err)
	}
}

func TestConsignmentRejectsDuplicateAndOversoldItems(t *testing.T) {
	consignment, err := NewConsignment("csn-2", "摊主", 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := consignment.AddItem("SKU-1", "商品", 1, 100); err != nil {
		t.Fatal(err)
	}
	if err := consignment.AddItem(" sku-1 ", "重复商品", 1, 100); !errors.Is(err, ErrValidation) {
		t.Fatalf("duplicate sku error = %v", err)
	}
	if err := consignment.Activate(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := consignment.RecordSale("one", "SKU-1", 2, time.Now()); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("oversold error = %v", err)
	}
}

func TestArithmeticRejectsOverflow(t *testing.T) {
	if _, err := ComputeLineCents(math.MaxInt64, 2); !errors.Is(err, ErrValidation) {
		t.Fatalf("line overflow error = %v", err)
	}
	if _, err := ComputeCommission(math.MaxInt64, 10_000); !errors.Is(err, ErrValidation) {
		t.Fatalf("commission overflow error = %v", err)
	}
}

func TestDraftEditingSummaryAndSettlementPreview(t *testing.T) {
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	consignment, err := NewConsignment("csn-edit", "旧摊主", 500, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := consignment.UpdateDetails("新摊主", 1250); err != nil {
		t.Fatal(err)
	}
	if err := consignment.AddItem("cup", "陶杯", 4, 1250); err != nil {
		t.Fatal(err)
	}
	if err := consignment.AddItem("remove", "待删除", 2, 300); err != nil {
		t.Fatal(err)
	}
	if err := consignment.UpdateItem("cup", " CUP-01 ", "釉面陶杯", 5, 1280); err != nil {
		t.Fatal(err)
	}
	if err := consignment.RemoveItem("remove"); err != nil {
		t.Fatal(err)
	}
	if consignment.SellerName != "新摊主" || consignment.CommissionBPS != 1250 || len(consignment.Items) != 1 || consignment.Items[0].SKU != "CUP-01" {
		t.Fatalf("unexpected edited consignment: %#v", consignment)
	}
	if err := consignment.Activate(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := consignment.RecordSale("sale-edit", "cup-01", 2, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	summary, err := consignment.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if summary.InitialQuantity != 5 || summary.SoldQuantity != 2 || summary.RemainingQuantity != 3 || summary.GrossSalesCents != 2560 || summary.SellThroughBPS != 4000 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	preview, err := consignment.PreviewSettlement(now.Add(3 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if preview.ProjectedVersion != consignment.Version+1 || preview.ReturnedQuantity != 3 || preview.CommissionCents != 320 || preview.SellerPayoutCents != 2240 {
		t.Fatalf("unexpected settlement preview: %#v", preview)
	}
	if err := consignment.UpdateDetails("不可修改", 0); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("active detail update error = %v", err)
	}
}

func TestRatioBPSHandlesLargeTotals(t *testing.T) {
	got, err := RatioBPS(math.MaxInt64-1, math.MaxInt64)
	if err != nil || got != 9999 {
		t.Fatalf("RatioBPS large result = %d, %v", got, err)
	}
}

func TestClonePreservesEmptyCollections(t *testing.T) {
	consignment, err := NewConsignment("csn-empty", "摊主", 0, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	clone := consignment.Clone()
	if clone.Items == nil || clone.Sales == nil {
		t.Fatalf("clone contains nil collections: %#v", clone)
	}
}
