package application

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"stallsettle/internal/domain"
)

type ListOptions struct {
	State domain.ConsignmentState
	Query string
	Sort  string
}

type ListResult struct {
	Consignments []domain.Consignment `json:"consignments"`
	Matched      int64                `json:"matched"`
	Overview     LedgerOverview       `json:"overview"`
}

type LedgerOverview struct {
	TotalConsignments      int64     `json:"total_consignments"`
	DraftConsignments      int64     `json:"draft_consignments"`
	ActiveConsignments     int64     `json:"active_consignments"`
	SettledConsignments    int64     `json:"settled_consignments"`
	ItemKinds              int64     `json:"item_kinds"`
	InitialQuantity        int64     `json:"initial_quantity"`
	SoldQuantity           int64     `json:"sold_quantity"`
	RemainingQuantity      int64     `json:"remaining_quantity"`
	GrossSalesCents        int64     `json:"gross_sales_cents"`
	CommissionCents        int64     `json:"commission_cents"`
	SellerPayoutCents      int64     `json:"seller_payout_cents"`
	RemainingRetailCents   int64     `json:"remaining_retail_cents"`
	SettledGrossCents      int64     `json:"settled_gross_cents"`
	SettledCommissionCents int64     `json:"settled_commission_cents"`
	SettledPayoutCents     int64     `json:"settled_payout_cents"`
	SellThroughBPS         int64     `json:"sell_through_bps"`
	SavedAt                time.Time `json:"saved_at"`
}

type ConsignmentDetail struct {
	Consignment domain.Consignment         `json:"consignment"`
	Summary     domain.ConsignmentSummary  `json:"summary"`
	Inventory   []domain.InventoryPosition `json:"inventory"`
	Settlement  *domain.SettlementPreview  `json:"settlement_preview,omitempty"`
}

func (s *Service) QueryConsignments(ctx context.Context, options ListOptions) (ListResult, error) {
	if err := contextError(ctx); err != nil {
		return ListResult{}, err
	}
	options, err := normalizeListOptions(options)
	if err != nil {
		return ListResult{}, err
	}
	s.mu.Lock()
	snapshot := s.snapshot.Clone()
	s.mu.Unlock()

	overview, err := summarizeSnapshot(ctx, snapshot)
	if err != nil {
		return ListResult{}, err
	}
	items := make([]domain.Consignment, 0, len(snapshot.Consignments))
	for _, consignment := range snapshot.Consignments {
		if err := contextError(ctx); err != nil {
			return ListResult{}, err
		}
		if options.State != "" && consignment.State != options.State {
			continue
		}
		if options.Query != "" && !matchesConsignment(consignment, options.Query) {
			continue
		}
		items = append(items, consignment)
	}
	sortConsignments(items, options.Sort)
	return ListResult{Consignments: items, Matched: int64(len(items)), Overview: overview}, nil
}

func (s *Service) GetDetail(ctx context.Context, id string) (ConsignmentDetail, error) {
	consignment, err := s.Get(ctx, id)
	if err != nil {
		return ConsignmentDetail{}, err
	}
	summary, err := consignment.Summary()
	if err != nil {
		return ConsignmentDetail{}, fmt.Errorf("%w: summarize consignment: %v", ErrStorage, err)
	}
	inventory, err := consignment.InventoryPositions()
	if err != nil {
		return ConsignmentDetail{}, fmt.Errorf("%w: calculate inventory: %v", ErrStorage, err)
	}
	detail := ConsignmentDetail{Consignment: consignment, Summary: summary, Inventory: inventory}
	if consignment.State == domain.StateActive {
		preview, previewErr := consignment.PreviewSettlement(s.clock.Now())
		if previewErr != nil {
			return ConsignmentDetail{}, fmt.Errorf("%w: preview settlement: %v", ErrStorage, previewErr)
		}
		detail.Settlement = &preview
	}
	return detail, nil
}

func normalizeListOptions(options ListOptions) (ListOptions, error) {
	if options.State != "" && options.State != domain.StateDraft && options.State != domain.StateActive && options.State != domain.StateSettled {
		return ListOptions{}, fmt.Errorf("%w: unknown state filter", domain.ErrValidation)
	}
	query := strings.TrimSpace(options.Query)
	if len(query) > 200 {
		return ListOptions{}, fmt.Errorf("%w: query is too long", domain.ErrValidation)
	}
	for _, r := range query {
		if unicode.IsControl(r) {
			return ListOptions{}, fmt.Errorf("%w: query contains control characters", domain.ErrValidation)
		}
	}
	options.Query = strings.ToLower(query)
	if options.Sort == "" {
		options.Sort = "newest"
	}
	if options.Sort != "newest" && options.Sort != "oldest" && options.Sort != "seller" {
		return ListOptions{}, fmt.Errorf("%w: unknown sort order", domain.ErrValidation)
	}
	return options, nil
}

func matchesConsignment(consignment domain.Consignment, query string) bool {
	if strings.Contains(strings.ToLower(consignment.ID), query) || strings.Contains(strings.ToLower(consignment.SellerName), query) {
		return true
	}
	for _, item := range consignment.Items {
		if strings.Contains(strings.ToLower(item.SKU), query) || strings.Contains(strings.ToLower(item.DisplaySKU), query) || strings.Contains(strings.ToLower(item.Name), query) {
			return true
		}
	}
	return false
}

func sortConsignments(items []domain.Consignment, order string) {
	sort.Slice(items, func(i, j int) bool {
		switch order {
		case "oldest":
			if items[i].CreatedAt.Equal(items[j].CreatedAt) {
				return items[i].ID < items[j].ID
			}
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		case "seller":
			left := strings.ToLower(items[i].SellerName)
			right := strings.ToLower(items[j].SellerName)
			if left == right {
				return items[i].ID < items[j].ID
			}
			return left < right
		default:
			if items[i].CreatedAt.Equal(items[j].CreatedAt) {
				return items[i].ID > items[j].ID
			}
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
	})
}

func summarizeSnapshot(ctx context.Context, snapshot domain.LedgerSnapshot) (LedgerOverview, error) {
	overview := LedgerOverview{SavedAt: snapshot.SavedAt}
	for _, consignment := range snapshot.Consignments {
		if err := contextError(ctx); err != nil {
			return LedgerOverview{}, err
		}
		summary, err := consignment.Summary()
		if err != nil {
			return LedgerOverview{}, fmt.Errorf("%w: summarize ledger: %v", ErrStorage, err)
		}
		if err := addOverview(&overview.TotalConsignments, 1, "consignment count"); err != nil {
			return LedgerOverview{}, err
		}
		switch consignment.State {
		case domain.StateDraft:
			err = addOverview(&overview.DraftConsignments, 1, "draft count")
		case domain.StateActive:
			err = addOverview(&overview.ActiveConsignments, 1, "active count")
		case domain.StateSettled:
			err = addOverview(&overview.SettledConsignments, 1, "settled count")
		}
		if err != nil {
			return LedgerOverview{}, err
		}
		values := []struct {
			total *int64
			value int64
			label string
		}{
			{&overview.ItemKinds, summary.ItemKinds, "item kinds"},
			{&overview.InitialQuantity, summary.InitialQuantity, "initial quantity"},
			{&overview.SoldQuantity, summary.SoldQuantity, "sold quantity"},
			{&overview.RemainingQuantity, summary.RemainingQuantity, "remaining quantity"},
			{&overview.GrossSalesCents, summary.GrossSalesCents, "gross sales"},
			{&overview.CommissionCents, summary.CommissionAccruedCents, "commission"},
			{&overview.SellerPayoutCents, summary.SellerAccruedCents, "seller payout"},
			{&overview.RemainingRetailCents, summary.RemainingRetailCents, "remaining retail"},
		}
		for _, value := range values {
			if err := addOverview(value.total, value.value, value.label); err != nil {
				return LedgerOverview{}, err
			}
		}
		if consignment.Receipt != nil {
			if err := addOverview(&overview.SettledGrossCents, consignment.Receipt.GrossCents, "settled gross"); err != nil {
				return LedgerOverview{}, err
			}
			if err := addOverview(&overview.SettledCommissionCents, consignment.Receipt.CommissionCents, "settled commission"); err != nil {
				return LedgerOverview{}, err
			}
			if err := addOverview(&overview.SettledPayoutCents, consignment.Receipt.SellerPayoutCents, "settled payout"); err != nil {
				return LedgerOverview{}, err
			}
		}
	}
	ratio, err := domain.RatioBPS(overview.SoldQuantity, overview.InitialQuantity)
	if err != nil {
		return LedgerOverview{}, fmt.Errorf("%w: calculate ledger ratio: %v", ErrStorage, err)
	}
	overview.SellThroughBPS = ratio
	return overview, nil
}

func addOverview(total *int64, value int64, label string) error {
	if value < 0 || *total > math.MaxInt64-value {
		return fmt.Errorf("%w: %s overflow", ErrStorage, label)
	}
	*total += value
	return nil
}
