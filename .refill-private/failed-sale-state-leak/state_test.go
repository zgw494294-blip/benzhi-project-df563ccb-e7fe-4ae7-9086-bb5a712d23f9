package failed_sale_state_leak_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stallsettle/internal/application"
	"stallsettle/internal/domain"
)

type memoryLedger struct {
	snapshot domain.LedgerSnapshot
	fail     error
}

func (l *memoryLedger) Load(context.Context) (domain.LedgerSnapshot, error) {
	if l.snapshot.Consignments == nil {
		return domain.NewEmptySnapshot(), nil
	}
	return l.snapshot.Clone(), nil
}

func (l *memoryLedger) Save(_ context.Context, snapshot domain.LedgerSnapshot) error {
	if l.fail != nil {
		return l.fail
	}
	l.snapshot = snapshot.Clone()
	return nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fixedIDs struct{}

func (fixedIDs) NewID() string { return "csn-failed-sale" }

func TestFailedSaleDoesNotLeakIntoMemory(t *testing.T) {
	ledger := &memoryLedger{}
	clock := fixedClock{now: time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)}
	service, err := application.Open(context.Background(), ledger, clock, fixedIDs{})
	if err != nil {
		t.Fatal(err)
	}
	consignment, err := service.CreateConsignment(context.Background(), "摊主", 0)
	if err != nil {
		t.Fatal(err)
	}
	consignment, err = service.AddItem(context.Background(), consignment.ID, consignment.Version, 2, 100, "SKU", "商品")
	if err != nil {
		t.Fatal(err)
	}
	consignment, err = service.Activate(context.Background(), consignment.ID, consignment.Version)
	if err != nil {
		t.Fatal(err)
	}
	ledger.fail = errors.New("存储不可用")
	if _, _, err := service.RecordSale(context.Background(), consignment.ID, consignment.Version, "sale-key", "SKU", 1); !errors.Is(err, application.ErrStorage) {
		t.Fatalf("销售保存失败返回 %v", err)
	}
	current, err := service.Get(context.Background(), consignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Items[0].SoldQuantity != 0 || len(current.Sales) != 0 || current.Version != consignment.Version {
		t.Fatalf("保存失败后内存状态发生变化：%#v", current)
	}
	assertCloneIsolation(t, clock.now)
}

func assertCloneIsolation(t *testing.T, now time.Time) {
	t.Helper()
	original, err := domain.NewConsignment("csn-clone", "原摊主", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := original.AddItem("SKU", "原商品", 1, 100); err != nil {
		t.Fatal(err)
	}
	if err := original.Activate(now); err != nil {
		t.Fatal(err)
	}
	if _, err := original.RecordSale("clone-sale", "SKU", 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := original.BuildReceipt(now); err != nil {
		t.Fatal(err)
	}
	clone := original.Clone()
	clone.Items[0].Name = "改后商品"
	clone.Sales[0].Quantity = 2
	clone.Receipt.SellerName = "改后摊主"
	clone.Receipt.Lines[0].Name = "改后凭据商品"
	if original.Items[0].Name != "原商品" || original.Sales[0].Quantity != 1 || original.Receipt.SellerName != "原摊主" || original.Receipt.Lines[0].Name != "原商品" {
		t.Fatalf("克隆结果与原寄售单共享可变数据：%#v", original)
	}
}
