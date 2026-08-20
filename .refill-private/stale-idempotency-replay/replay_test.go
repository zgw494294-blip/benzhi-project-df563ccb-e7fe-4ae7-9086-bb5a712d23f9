package stale_idempotency_replay_test

import (
	"context"
	"testing"
	"time"

	"stallsettle/internal/application"
	"stallsettle/internal/domain"
)

type memoryLedger struct{ snapshot domain.LedgerSnapshot }

func (l *memoryLedger) Load(context.Context) (domain.LedgerSnapshot, error) {
	if l.snapshot.Consignments == nil {
		return domain.NewEmptySnapshot(), nil
	}
	return l.snapshot.Clone(), nil
}

func (l *memoryLedger) Save(_ context.Context, snapshot domain.LedgerSnapshot) error {
	l.snapshot = snapshot.Clone()
	return nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fixedIDs struct{}

func (fixedIDs) NewID() string { return "csn-replay" }

func TestOlderSaleIdempotencyKeyStillReplays(t *testing.T) {
	service, err := application.Open(context.Background(), &memoryLedger{}, fixedClock{now: time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)}, fixedIDs{})
	if err != nil {
		t.Fatal(err)
	}
	consignment, err := service.CreateConsignment(context.Background(), "摊主", 0)
	if err != nil {
		t.Fatal(err)
	}
	consignment, err = service.AddItem(context.Background(), consignment.ID, consignment.Version, 3, 100, "SKU", "商品")
	if err != nil {
		t.Fatal(err)
	}
	consignment, err = service.Activate(context.Background(), consignment.ID, consignment.Version)
	if err != nil {
		t.Fatal(err)
	}
	firstRequestVersion := consignment.Version
	consignment, first, err := service.RecordSale(context.Background(), consignment.ID, consignment.Version, "first-key", "SKU", 1)
	if err != nil {
		t.Fatal(err)
	}
	consignment, _, err = service.RecordSale(context.Background(), consignment.ID, consignment.Version, "second-key", "SKU", 1)
	if err != nil {
		t.Fatal(err)
	}
	replayed, replayedEvent, err := service.RecordSale(context.Background(), consignment.ID, firstRequestVersion, "first-key", "sku", 1)
	if err != nil {
		t.Fatalf("较早的幂等键重放失败：%v", err)
	}
	if replayedEvent != first || len(replayed.Sales) != 2 || replayed.Version != consignment.Version {
		t.Fatalf("重放改变了销售结果：%#v %#v", replayed, replayedEvent)
	}
}
