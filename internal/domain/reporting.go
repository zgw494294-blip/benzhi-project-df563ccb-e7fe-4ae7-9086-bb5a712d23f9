package domain

import (
	"fmt"
	"math"
	"math/bits"
	"time"
)

// InventoryPosition is a calculated, read-only view of one consigned item.
type InventoryPosition struct {
	SKU                  string `json:"sku"`
	DisplaySKU           string `json:"display_sku"`
	Name                 string `json:"name"`
	InitialQuantity      int64  `json:"initial_quantity"`
	SoldQuantity         int64  `json:"sold_quantity"`
	RemainingQuantity    int64  `json:"remaining_quantity"`
	UnitPriceCents       int64  `json:"unit_price_cents"`
	InitialRetailCents   int64  `json:"initial_retail_cents"`
	SalesCents           int64  `json:"sales_cents"`
	RemainingRetailCents int64  `json:"remaining_retail_cents"`
	SellThroughBPS       int64  `json:"sell_through_bps"`
}

// ConsignmentSummary contains operational totals that can be displayed without
// changing the aggregate or reconstructing amounts in a transport adapter.
type ConsignmentSummary struct {
	ConsignmentID          string           `json:"consignment_id"`
	SellerName             string           `json:"seller_name"`
	State                  ConsignmentState `json:"state"`
	Version                int64            `json:"version"`
	ItemKinds              int64            `json:"item_kinds"`
	InitialQuantity        int64            `json:"initial_quantity"`
	SoldQuantity           int64            `json:"sold_quantity"`
	RemainingQuantity      int64            `json:"remaining_quantity"`
	SaleEvents             int64            `json:"sale_events"`
	InitialRetailCents     int64            `json:"initial_retail_cents"`
	GrossSalesCents        int64            `json:"gross_sales_cents"`
	RemainingRetailCents   int64            `json:"remaining_retail_cents"`
	CommissionAccruedCents int64            `json:"commission_accrued_cents"`
	SellerAccruedCents     int64            `json:"seller_accrued_cents"`
	SellThroughBPS         int64            `json:"sell_through_bps"`
	LastSaleAt             *time.Time       `json:"last_sale_at,omitempty"`
}

// SettlementPreview is the current settlement projection for an active
// consignment. It becomes a receipt only after the versioned write commits.
type SettlementPreview struct {
	ConsignmentID     string        `json:"consignment_id"`
	CurrentVersion    int64         `json:"current_version"`
	ProjectedVersion  int64         `json:"projected_version"`
	Lines             []ReceiptLine `json:"lines"`
	GrossCents        int64         `json:"gross_cents"`
	CommissionCents   int64         `json:"commission_cents"`
	SellerPayoutCents int64         `json:"seller_payout_cents"`
	ReturnedQuantity  int64         `json:"returned_quantity"`
	CalculatedAt      time.Time     `json:"calculated_at"`
}

func (c Consignment) InventoryPositions() ([]InventoryPosition, error) {
	positions := make([]InventoryPosition, 0, len(c.Items))
	for _, item := range c.Items {
		if item.InitialQuantity <= 0 || item.SoldQuantity < 0 || item.SoldQuantity > item.InitialQuantity {
			return nil, fmt.Errorf("%w: item quantities are invalid", ErrValidation)
		}
		initialRetail, err := ComputeLineCents(item.UnitPriceCents, item.InitialQuantity)
		if err != nil {
			return nil, err
		}
		remaining := item.InitialQuantity - item.SoldQuantity
		sales := int64(0)
		if item.SoldQuantity > 0 {
			sales, err = ComputeLineCents(item.UnitPriceCents, item.SoldQuantity)
			if err != nil {
				return nil, err
			}
		}
		remainingRetail := int64(0)
		if remaining > 0 {
			remainingRetail, err = ComputeLineCents(item.UnitPriceCents, remaining)
			if err != nil {
				return nil, err
			}
		}
		sellThrough, err := RatioBPS(item.SoldQuantity, item.InitialQuantity)
		if err != nil {
			return nil, err
		}
		positions = append(positions, InventoryPosition{
			SKU:                  item.SKU,
			DisplaySKU:           item.DisplaySKU,
			Name:                 item.Name,
			InitialQuantity:      item.InitialQuantity,
			SoldQuantity:         item.SoldQuantity,
			RemainingQuantity:    remaining,
			UnitPriceCents:       item.UnitPriceCents,
			InitialRetailCents:   initialRetail,
			SalesCents:           sales,
			RemainingRetailCents: remainingRetail,
			SellThroughBPS:       sellThrough,
		})
	}
	return positions, nil
}

func (c Consignment) Summary() (ConsignmentSummary, error) {
	if err := c.Validate(); err != nil {
		return ConsignmentSummary{}, err
	}
	positions, err := c.InventoryPositions()
	if err != nil {
		return ConsignmentSummary{}, err
	}
	summary := ConsignmentSummary{
		ConsignmentID: c.ID,
		SellerName:    c.SellerName,
		State:         c.State,
		Version:       c.Version,
		ItemKinds:     int64(len(positions)),
		SaleEvents:    int64(len(c.Sales)),
	}
	for _, position := range positions {
		if err := addTo(&summary.InitialQuantity, position.InitialQuantity, "initial quantity"); err != nil {
			return ConsignmentSummary{}, err
		}
		if err := addTo(&summary.SoldQuantity, position.SoldQuantity, "sold quantity"); err != nil {
			return ConsignmentSummary{}, err
		}
		if err := addTo(&summary.RemainingQuantity, position.RemainingQuantity, "remaining quantity"); err != nil {
			return ConsignmentSummary{}, err
		}
		if err := addTo(&summary.InitialRetailCents, position.InitialRetailCents, "initial retail amount"); err != nil {
			return ConsignmentSummary{}, err
		}
		if err := addTo(&summary.GrossSalesCents, position.SalesCents, "gross sales amount"); err != nil {
			return ConsignmentSummary{}, err
		}
		if err := addTo(&summary.RemainingRetailCents, position.RemainingRetailCents, "remaining retail amount"); err != nil {
			return ConsignmentSummary{}, err
		}
	}
	summary.SellThroughBPS, err = RatioBPS(summary.SoldQuantity, summary.InitialQuantity)
	if err != nil {
		return ConsignmentSummary{}, err
	}
	summary.CommissionAccruedCents, err = ComputeCommission(summary.GrossSalesCents, c.CommissionBPS)
	if err != nil {
		return ConsignmentSummary{}, err
	}
	summary.SellerAccruedCents = summary.GrossSalesCents - summary.CommissionAccruedCents
	if len(c.Sales) > 0 {
		last := c.Sales[0].RecordedAt
		for _, sale := range c.Sales[1:] {
			if sale.RecordedAt.After(last) {
				last = sale.RecordedAt
			}
		}
		summary.LastSaleAt = &last
	}
	return summary, nil
}

func (c Consignment) PreviewSettlement(at time.Time) (SettlementPreview, error) {
	if c.State != StateActive {
		return SettlementPreview{}, fmt.Errorf("%w: only active consignments can be previewed", ErrStateConflict)
	}
	if c.Receipt != nil {
		return SettlementPreview{}, fmt.Errorf("%w: settlement receipt already exists", ErrStateConflict)
	}
	if at.IsZero() || at.Before(c.ActivatedAt) {
		return SettlementPreview{}, fmt.Errorf("%w: settlement time is invalid", ErrValidation)
	}
	if c.Version == math.MaxInt64 {
		return SettlementPreview{}, fmt.Errorf("%w: version overflow", ErrValidation)
	}
	positions, err := c.InventoryPositions()
	if err != nil {
		return SettlementPreview{}, err
	}
	preview := SettlementPreview{
		ConsignmentID:    c.ID,
		CurrentVersion:   c.Version,
		ProjectedVersion: c.Version + 1,
		Lines:            make([]ReceiptLine, 0, len(positions)),
		CalculatedAt:     at,
	}
	for _, position := range positions {
		if err := addTo(&preview.GrossCents, position.SalesCents, "settlement gross amount"); err != nil {
			return SettlementPreview{}, err
		}
		if err := addTo(&preview.ReturnedQuantity, position.RemainingQuantity, "returned quantity"); err != nil {
			return SettlementPreview{}, err
		}
		preview.Lines = append(preview.Lines, ReceiptLine{
			SKU:              position.SKU,
			DisplaySKU:       position.DisplaySKU,
			Name:             position.Name,
			SoldQuantity:     position.SoldQuantity,
			ReturnedQuantity: position.RemainingQuantity,
			UnitPriceCents:   position.UnitPriceCents,
			SalesCents:       position.SalesCents,
		})
	}
	commission, err := ComputeCommission(preview.GrossCents, c.CommissionBPS)
	if err != nil {
		return SettlementPreview{}, err
	}
	preview.CommissionCents = commission
	preview.SellerPayoutCents = preview.GrossCents - preview.CommissionCents
	return preview, nil
}

func RatioBPS(part, total int64) (int64, error) {
	if part < 0 || total < 0 || part > total {
		return 0, fmt.Errorf("%w: ratio values are invalid", ErrValidation)
	}
	if total == 0 {
		return 0, nil
	}
	high, low := bits.Mul64(uint64(part), 10_000)
	quotient, _ := bits.Div64(high, low, uint64(total))
	if quotient > 10_000 {
		return 0, fmt.Errorf("%w: ratio is outside the allowed range", ErrValidation)
	}
	return int64(quotient), nil
}

func addTo(total *int64, value int64, label string) error {
	if value < 0 || *total > math.MaxInt64-value {
		return fmt.Errorf("%w: %s overflow", ErrValidation, label)
	}
	*total += value
	return nil
}
