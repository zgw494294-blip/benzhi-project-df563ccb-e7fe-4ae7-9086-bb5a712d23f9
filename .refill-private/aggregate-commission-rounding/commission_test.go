package aggregate_commission_rounding_test

import (
	"testing"
	"time"

	"stallsettle/internal/domain"
)

func TestCommissionRoundsOnceOnAggregateGross(t *testing.T) {
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	consignment, err := domain.NewConsignment("csn-commission", "摊主", 5000, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, sku := range []string{"A", "B"} {
		if err := consignment.AddItem(sku, "商品 "+sku, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := consignment.Activate(now); err != nil {
		t.Fatal(err)
	}
	if _, err := consignment.RecordSale("sale-a", "A", 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := consignment.RecordSale("sale-b", "B", 1, now); err != nil {
		t.Fatal(err)
	}
	receipt, err := consignment.BuildReceipt(now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.GrossCents != 2 || receipt.CommissionCents != 1 || receipt.SellerPayoutCents != 1 {
		t.Fatalf("两笔一分钱销售的结算结果为：%#v", receipt)
	}
}
