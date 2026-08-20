package sale_resulting_version_gap_test

import (
	"testing"
	"time"

	"stallsettle/internal/domain"
)

func TestSaleResultingVersionMustMatchMutationSequence(t *testing.T) {
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	consignment, err := domain.NewConsignment("csn-version", "摊主", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := consignment.AddItem("SKU", "商品", 2, 100); err != nil {
		t.Fatal(err)
	}
	if err := consignment.Activate(now); err != nil {
		t.Fatal(err)
	}
	if _, err := consignment.RecordSale("sale-key", "SKU", 1, now); err != nil {
		t.Fatal(err)
	}
	consignment.Sales[0].ResultingVersion = 1
	if err := consignment.Validate(); err == nil {
		t.Fatal("销售事件的 resulting_version 与实际变更序列不符时仍通过校验")
	}
}
