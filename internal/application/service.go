package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"stallsettle/internal/domain"
)

var (
	ErrVersionConflict = errors.New("application version conflict")
	ErrStorage         = errors.New("application storage failure")
)

type Service struct {
	mu       sync.Mutex
	ledger   Ledger
	clock    Clock
	ids      IDGenerator
	snapshot domain.LedgerSnapshot
}

func Open(ctx context.Context, ledger Ledger, clock Clock, ids IDGenerator) (*Service, error) {
	if ledger == nil {
		return nil, fmt.Errorf("%w: ledger is required", ErrStorage)
	}
	if clock == nil {
		clock = RealClock{}
	}
	if ids == nil {
		ids = &SequentialIDGenerator{}
	}
	snapshot, err := ledger.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: load ledger: %v", ErrStorage, err)
	}
	if snapshot.Consignments == nil {
		snapshot = domain.NewEmptySnapshot()
	}
	if err := snapshot.Validate(); err != nil {
		return nil, fmt.Errorf("%w: validate ledger: %v", ErrStorage, err)
	}
	return &Service{ledger: ledger, clock: clock, ids: ids, snapshot: snapshot}, nil
}

func (s *Service) CreateConsignment(ctx context.Context, sellerName string, commissionBPS int64) (domain.Consignment, error) {
	if err := contextError(ctx); err != nil {
		return domain.Consignment{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return domain.Consignment{}, err
	}
	var consignment domain.Consignment
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		id := s.ids.NewID()
		if _, exists := s.snapshot.Consignments[id]; exists {
			continue
		}
		consignment, err = domain.NewConsignment(id, sellerName, commissionBPS, s.clock.Now())
		if err != nil {
			return domain.Consignment{}, err
		}
		break
	}
	if consignment.ID == "" {
		return domain.Consignment{}, fmt.Errorf("%w: unable to allocate id", ErrStorage)
	}
	next := s.snapshot.Clone()
	next.Consignments[consignment.ID] = consignment
	next.SavedAt = s.clock.Now()
	if err := s.saveLocked(ctx, next); err != nil {
		return domain.Consignment{}, err
	}
	s.snapshot = next
	return consignment, nil
}

func (s *Service) AddItem(ctx context.Context, id string, expectedVersion, quantity, unitPriceCents int64, rawSKU, name string) (domain.Consignment, error) {
	return s.mutate(ctx, id, expectedVersion, func(consignment *domain.Consignment) error {
		return consignment.AddItem(rawSKU, name, quantity, unitPriceCents)
	})
}

func (s *Service) UpdateConsignment(ctx context.Context, id string, expectedVersion int64, sellerName string, commissionBPS int64) (domain.Consignment, error) {
	return s.mutate(ctx, id, expectedVersion, func(consignment *domain.Consignment) error {
		return consignment.UpdateDetails(sellerName, commissionBPS)
	})
}

func (s *Service) UpdateItem(ctx context.Context, id string, expectedVersion, quantity, unitPriceCents int64, currentSKU, newSKU, name string) (domain.Consignment, error) {
	return s.mutate(ctx, id, expectedVersion, func(consignment *domain.Consignment) error {
		return consignment.UpdateItem(currentSKU, newSKU, name, quantity, unitPriceCents)
	})
}

func (s *Service) RemoveItem(ctx context.Context, id string, expectedVersion int64, rawSKU string) (domain.Consignment, error) {
	return s.mutate(ctx, id, expectedVersion, func(consignment *domain.Consignment) error {
		return consignment.RemoveItem(rawSKU)
	})
}

func (s *Service) Activate(ctx context.Context, id string, expectedVersion int64) (domain.Consignment, error) {
	return s.mutate(ctx, id, expectedVersion, func(consignment *domain.Consignment) error {
		return consignment.Activate(s.clock.Now())
	})
}

func (s *Service) RecordSale(ctx context.Context, id string, expectedVersion int64, rawKey, rawSKU string, quantity int64) (domain.Consignment, domain.SaleEvent, error) {
	if err := contextError(ctx); err != nil {
		return domain.Consignment{}, domain.SaleEvent{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return domain.Consignment{}, domain.SaleEvent{}, err
	}
	current, ok := s.snapshot.Consignments[id]
	if !ok {
		return domain.Consignment{}, domain.SaleEvent{}, domain.ErrNotFound
	}
	key, err := domain.NormalizeIdempotencyKey(rawKey)
	if err != nil {
		return domain.Consignment{}, domain.SaleEvent{}, err
	}
	sku, _, err := domain.NormalizeSKU(rawSKU)
	if err != nil {
		return domain.Consignment{}, domain.SaleEvent{}, err
	}
	if previous, exists := current.FindSale(key); exists {
		if previous.SKU != sku || previous.Quantity != quantity {
			return domain.Consignment{}, domain.SaleEvent{}, domain.ErrIdempotencyConflict
		}
		return current, previous, nil
	}
	if expectedVersion != current.Version {
		return domain.Consignment{}, domain.SaleEvent{}, ErrVersionConflict
	}
	candidate := current.Clone()
	event, err := candidate.RecordSale(key, sku, quantity, s.clock.Now())
	if err != nil {
		return domain.Consignment{}, domain.SaleEvent{}, err
	}
	next := s.snapshot.Clone()
	next.Consignments[id] = candidate
	next.SavedAt = s.clock.Now()
	if err := s.saveLocked(ctx, next); err != nil {
		return domain.Consignment{}, domain.SaleEvent{}, err
	}
	s.snapshot = next
	return candidate, event, nil
}

func (s *Service) Settle(ctx context.Context, id string, expectedVersion int64) (domain.Consignment, domain.SettlementReceipt, error) {
	if err := contextError(ctx); err != nil {
		return domain.Consignment{}, domain.SettlementReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return domain.Consignment{}, domain.SettlementReceipt{}, err
	}
	current, ok := s.snapshot.Consignments[id]
	if !ok {
		return domain.Consignment{}, domain.SettlementReceipt{}, domain.ErrNotFound
	}
	if expectedVersion != current.Version {
		return domain.Consignment{}, domain.SettlementReceipt{}, ErrVersionConflict
	}
	candidate := current.Clone()
	receipt, err := candidate.BuildReceipt(s.clock.Now())
	if err != nil {
		return domain.Consignment{}, domain.SettlementReceipt{}, err
	}
	next := s.snapshot.Clone()
	next.Consignments[id] = candidate
	next.SavedAt = s.clock.Now()
	if err := s.saveLocked(ctx, next); err != nil {
		return domain.Consignment{}, domain.SettlementReceipt{}, err
	}
	s.snapshot = next
	return candidate, receipt, nil
}

func (s *Service) Get(ctx context.Context, id string) (domain.Consignment, error) {
	if err := contextError(ctx); err != nil {
		return domain.Consignment{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	consignment, ok := s.snapshot.Consignments[id]
	if !ok {
		return domain.Consignment{}, domain.ErrNotFound
	}
	return consignment.Clone(), nil
}

func (s *Service) List(ctx context.Context) ([]domain.Consignment, error) {
	result, err := s.QueryConsignments(ctx, ListOptions{Sort: "oldest"})
	return result.Consignments, err
}

func (s *Service) mutate(ctx context.Context, id string, expectedVersion int64, change func(*domain.Consignment) error) (domain.Consignment, error) {
	if err := contextError(ctx); err != nil {
		return domain.Consignment{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return domain.Consignment{}, err
	}
	current, ok := s.snapshot.Consignments[id]
	if !ok {
		return domain.Consignment{}, domain.ErrNotFound
	}
	if expectedVersion != current.Version {
		return domain.Consignment{}, ErrVersionConflict
	}
	candidate := current.Clone()
	if err := change(&candidate); err != nil {
		return domain.Consignment{}, err
	}
	next := s.snapshot.Clone()
	next.Consignments[id] = candidate
	next.SavedAt = s.clock.Now()
	saveContext := ctx
	if err := contextError(ctx); err != nil {
		saveContext = context.Background()
	}
	if err := s.saveLocked(saveContext, next); err != nil {
		return domain.Consignment{}, err
	}
	s.snapshot = next
	return candidate, nil
}

func (s *Service) saveLocked(ctx context.Context, next domain.LedgerSnapshot) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return fmt.Errorf("%w: candidate validation: %v", ErrStorage, err)
	}
	if err := s.ledger.Save(ctx, next); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("%w: save ledger: %v", ErrStorage, err)
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

type SequentialIDGenerator struct {
	sequence atomic.Uint64
}

func (g *SequentialIDGenerator) NewID() string {
	n := g.sequence.Add(1)
	return fmt.Sprintf("csn-%d-%d", time.Now().UTC().UnixNano(), n)
}
