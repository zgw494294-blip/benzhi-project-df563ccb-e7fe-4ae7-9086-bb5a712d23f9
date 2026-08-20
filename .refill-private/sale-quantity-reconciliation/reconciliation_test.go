package sale_quantity_reconciliation_test

import (
	"testing"
	"time"

	"stallsettle/internal/domain"
)

func TestItemSoldQuantityMatchesSaleHistory(t *testing.T) {
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	consignment, err := domain.NewConsignment("csn-sale-quantity", "摊主", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := consignment.AddItem("SKU", "商品", 5, 100); err != nil {
		t.Fatal(err)
	}
	if err := consignment.Activate(now); err != nil {
		t.Fatal(err)
	}
	if _, err := consignment.RecordSale("sale-key", "SKU", 2, now); err != nil {
		t.Fatal(err)
	}
	consignment.Items[0].SoldQuantity = 1
	if err := consignment.Validate(); err == nil {
		t.Fatal("商品累计售出数量与销售历史不一致时仍通过校验")
	}
}
