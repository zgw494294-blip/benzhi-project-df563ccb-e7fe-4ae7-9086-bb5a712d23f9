package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"stallsettle/internal/domain"
)

type memoryLedger struct {
	mu       sync.Mutex
	snapshot domain.LedgerSnapshot
	fail     error
	saves    int
}

func (l *memoryLedger) Load(context.Context) (domain.LedgerSnapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.snapshot.Consignments == nil {
		return domain.NewEmptySnapshot(), nil
	}
	return l.snapshot.Clone(), nil
}

func (l *memoryLedger) Save(_ context.Context, snapshot domain.LedgerSnapshot) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.saves++
	if l.fail != nil {
		return l.fail
	}
	l.snapshot = snapshot.Clone()
	return nil
}

type fixedClock struct{ value time.Time }

func (c fixedClock) Now() time.Time { return c.value }

type fixedIDs struct{ next int }

func (g *fixedIDs) NewID() string {
	g.next++
	return "csn-test-" + string(rune('0'+g.next))
}

func newTestService(t *testing.T, ledger *memoryLedger) *Service {
	t.Helper()
	service, err := Open(context.Background(), ledger, fixedClock{value: time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)}, &fixedIDs{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestServicePreservesStateWhenSaveFails(t *testing.T) {
	ledger := &memoryLedger{}
	service := newTestService(t, ledger)
	consignment, err := service.CreateConsignment(context.Background(), "摊主", 500)
	if err != nil {
		t.Fatal(err)
	}
	ledger.mu.Lock()
	ledger.fail = errors.New("存储暂不可用")
	ledger.mu.Unlock()
	if _, err := service.AddItem(context.Background(), consignment.ID, consignment.Version, 1, 100, "SKU", "商品"); !errors.Is(err, ErrStorage) {
		t.Fatalf("save failure error = %v", err)
	}
	current, err := service.Get(context.Background(), consignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != consignment.Version || len(current.Items) != 0 {
		t.Fatalf("state changed after failed save: %#v", current)
	}
}

func TestServiceVersionAndIdempotencyRules(t *testing.T) {
	ledger := &memoryLedger{}
	service := newTestService(t, ledger)
	created, err := service.CreateConsignment(context.Background(), "摊主", 1000)
	if err != nil {
		t.Fatal(err)
	}
	created, err = service.AddItem(context.Background(), created.ID, created.Version, 3, 100, "SKU-1", "商品")
	if err != nil {
		t.Fatal(err)
	}
	created, err = service.Activate(context.Background(), created.ID, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	first, event, err := service.RecordSale(context.Background(), created.ID, created.Version, "key-1", "sku-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	replay, replayEvent, err := service.RecordSale(context.Background(), created.ID, created.Version, "key-1", "SKU-1", 1)
	if err != nil || replay.Version != first.Version || replayEvent != event || len(replay.Sales) != 1 {
		t.Fatalf("replay changed result: %#v %#v %v", replay, replayEvent, err)
	}
	if _, _, err := service.RecordSale(context.Background(), created.ID, created.Version, "key-1", "SKU-1", 2); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict error = %v", err)
	}
	if _, _, err := service.RecordSale(context.Background(), created.ID, created.Version, "key-2", "SKU-1", 1); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version conflict error = %v", err)
	}
}

func TestServiceConcurrentWritesAllowOneVersion(t *testing.T) {
	ledger := &memoryLedger{}
	service := newTestService(t, ledger)
	created, err := service.CreateConsignment(context.Background(), "摊主", 0)
	if err != nil {
		t.Fatal(err)
	}
	created, err = service.AddItem(context.Background(), created.ID, created.Version, 1, 100, "SKU", "商品")
	if err != nil {
		t.Fatal(err)
	}
	created, err = service.Activate(context.Background(), created.ID, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for _, key := range []string{"one", "two"} {
		wait.Add(1)
		go func(key string) {
			defer wait.Done()
			_, _, callErr := service.RecordSale(context.Background(), created.ID, created.Version, key, "SKU", 1)
			errorsSeen <- callErr
		}(key)
	}
	wait.Wait()
	close(errorsSeen)
	var success, conflicts int
	for callErr := range errorsSeen {
		if callErr == nil {
			success++
		} else if errors.Is(callErr, ErrVersionConflict) || errors.Is(callErr, domain.ErrInsufficientStock) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent error = %v", callErr)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
}

func TestServiceRejectsCanceledWriteBeforeCommit(t *testing.T) {
	ledger := &memoryLedger{}
	service := newTestService(t, ledger)
	created, err := service.CreateConsignment(context.Background(), "摊主", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.AddItem(ctx, created.ID, created.Version, 1, 100, "SKU", "商品"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write error = %v", err)
	}
	current, err := service.Get(context.Background(), created.ID)
	if err != nil || len(current.Items) != 0 {
		t.Fatalf("canceled write changed state: %#v %v", current, err)
	}
}

func TestServiceQueriesDetailsAndLedgerOverview(t *testing.T) {
	ledger := &memoryLedger{}
	service := newTestService(t, ledger)
	first, err := service.CreateConsignment(context.Background(), "青山陶坊", 1000)
	if err != nil {
		t.Fatal(err)
	}
	first, err = service.UpdateConsignment(context.Background(), first.ID, first.Version, "青山陶作", 1000)
	if err != nil {
		t.Fatal(err)
	}
	first, err = service.AddItem(context.Background(), first.ID, first.Version, 4, 500, "CUP", "陶杯")
	if err != nil {
		t.Fatal(err)
	}
	first, err = service.Activate(context.Background(), first.ID, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err = service.RecordSale(context.Background(), first.ID, first.Version, "sale-query", "cup", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateConsignment(context.Background(), "木作摊位", 500); err != nil {
		t.Fatal(err)
	}
	result, err := service.QueryConsignments(context.Background(), ListOptions{State: domain.StateActive, Query: "陶杯", Sort: "seller"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 1 || result.Consignments[0].ID != first.ID || result.Overview.TotalConsignments != 2 || result.Overview.ActiveConsignments != 1 || result.Overview.DraftConsignments != 1 || result.Overview.GrossSalesCents != 1000 {
		t.Fatalf("unexpected query result: %#v", result)
	}
	detail, err := service.GetDetail(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary.SoldQuantity != 2 || len(detail.Inventory) != 1 || detail.Settlement == nil || detail.Settlement.SellerPayoutCents != 900 {
		t.Fatalf("unexpected detail: %#v", detail)
	}
	if _, err := service.QueryConsignments(context.Background(), ListOptions{State: "unknown"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown state filter error = %v", err)
	}
}
