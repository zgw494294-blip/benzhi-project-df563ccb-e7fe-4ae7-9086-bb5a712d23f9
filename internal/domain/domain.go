package domain

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	LedgerFormatVersion int64 = 1
	MaxCommissionBPS    int64 = 10_000
	MaxQuantity         int64 = 1_000_000
	MaxUnitPriceCents   int64 = 10_000_000_000
)

var (
	ErrValidation          = errors.New("domain validation failed")
	ErrStateConflict       = errors.New("domain state conflict")
	ErrNotFound            = errors.New("domain item not found")
	ErrIdempotencyConflict = errors.New("domain idempotency conflict")
	ErrInsufficientStock   = errors.New("domain insufficient stock")
)

type ConsignmentState string

const (
	StateDraft   ConsignmentState = "draft"
	StateActive  ConsignmentState = "active"
	StateSettled ConsignmentState = "settled"
)

type Consignment struct {
	ID            string             `json:"id"`
	SellerName    string             `json:"seller_name"`
	CommissionBPS int64              `json:"commission_bps"`
	State         ConsignmentState   `json:"state"`
	Version       int64              `json:"version"`
	Items         []ConsignmentItem  `json:"items"`
	Sales         []SaleEvent        `json:"sales"`
	CreatedAt     time.Time          `json:"created_at"`
	ActivatedAt   time.Time          `json:"activated_at,omitempty"`
	SettledAt     time.Time          `json:"settled_at,omitempty"`
	Receipt       *SettlementReceipt `json:"receipt,omitempty"`
}

type ConsignmentItem struct {
	SKU             string `json:"sku"`
	DisplaySKU      string `json:"display_sku"`
	Name            string `json:"name"`
	InitialQuantity int64  `json:"initial_quantity"`
	SoldQuantity    int64  `json:"sold_quantity"`
	UnitPriceCents  int64  `json:"unit_price_cents"`
}

type SaleEvent struct {
	IdempotencyKey   string    `json:"idempotency_key"`
	SKU              string    `json:"sku"`
	Quantity         int64     `json:"quantity"`
	GrossCents       int64     `json:"gross_cents"`
	RecordedAt       time.Time `json:"recorded_at"`
	ResultingVersion int64     `json:"resulting_version"`
}

type ReceiptLine struct {
	SKU              string `json:"sku"`
	DisplaySKU       string `json:"display_sku"`
	Name             string `json:"name"`
	SoldQuantity     int64  `json:"sold_quantity"`
	ReturnedQuantity int64  `json:"returned_quantity"`
	UnitPriceCents   int64  `json:"unit_price_cents"`
	SalesCents       int64  `json:"sales_cents"`
}

type SettlementReceipt struct {
	ConsignmentID     string        `json:"consignment_id"`
	SellerName        string        `json:"seller_name"`
	Lines             []ReceiptLine `json:"lines"`
	GrossCents        int64         `json:"gross_cents"`
	CommissionCents   int64         `json:"commission_cents"`
	SellerPayoutCents int64         `json:"seller_payout_cents"`
	SettledAt         time.Time     `json:"settled_at"`
	FinalVersion      int64         `json:"final_version"`
}

type LedgerSnapshot struct {
	FormatVersion int64                  `json:"format_version"`
	Consignments  map[string]Consignment `json:"consignments"`
	SavedAt       time.Time              `json:"saved_at"`
}

func NewEmptySnapshot() LedgerSnapshot {
	return LedgerSnapshot{FormatVersion: LedgerFormatVersion, Consignments: make(map[string]Consignment)}
}

func NewConsignment(id, sellerName string, commissionBPS int64, now time.Time) (Consignment, error) {
	if strings.TrimSpace(id) == "" {
		return Consignment{}, fmt.Errorf("%w: id is required", ErrValidation)
	}
	seller, err := cleanName(sellerName, "seller name")
	if err != nil {
		return Consignment{}, err
	}
	if commissionBPS < 0 || commissionBPS > MaxCommissionBPS {
		return Consignment{}, fmt.Errorf("%w: commission must be between 0 and 10000", ErrValidation)
	}
	if now.IsZero() {
		return Consignment{}, fmt.Errorf("%w: creation time is required", ErrValidation)
	}
	return Consignment{
		ID:            id,
		SellerName:    seller,
		CommissionBPS: commissionBPS,
		State:         StateDraft,
		Version:       1,
		Items:         make([]ConsignmentItem, 0),
		Sales:         make([]SaleEvent, 0),
		CreatedAt:     now,
	}, nil
}

func NormalizeSKU(raw string) (string, string, error) {
	display := strings.TrimSpace(raw)
	if display == "" {
		return "", "", fmt.Errorf("%w: sku is required", ErrValidation)
	}
	if len(display) > 64 {
		return "", "", fmt.Errorf("%w: sku is too long", ErrValidation)
	}
	for _, r := range display {
		if unicode.IsControl(r) {
			return "", "", fmt.Errorf("%w: sku contains control characters", ErrValidation)
		}
	}
	normalized := strings.ToUpper(display)
	if len(normalized) > 64 {
		return "", "", fmt.Errorf("%w: sku is too long", ErrValidation)
	}
	return normalized, display, nil
}

func NormalizeIdempotencyKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", fmt.Errorf("%w: idempotency key is required", ErrValidation)
	}
	if len(key) > 128 {
		return "", fmt.Errorf("%w: idempotency key is too long", ErrValidation)
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: idempotency key contains control characters", ErrValidation)
		}
	}
	return key, nil
}

func ParsePriceCents(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 24 || strings.ContainsAny(value, "+-eE") {
		return 0, fmt.Errorf("%w: price must be a decimal amount", ErrValidation)
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && len(parts[1]) > 2) {
		return 0, fmt.Errorf("%w: price must use at most two decimal places", ErrValidation)
	}
	if _, err := parseDigits(parts[0]); err != nil {
		return 0, fmt.Errorf("%w: price must contain digits only", ErrValidation)
	}
	if len(parts) == 2 {
		fraction := parts[1]
		if fraction == "" {
			fraction = "0"
		}
		if _, err := parseDigits(fraction); err != nil {
			return 0, fmt.Errorf("%w: price must contain digits only", ErrValidation)
		}
	}
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil || amount <= 0 || amount > float64(MaxUnitPriceCents)/100 {
		return 0, fmt.Errorf("%w: price must be positive and within the allowed limit", ErrValidation)
	}
	return int64(amount * 100), nil
}

func ValidatePriceCents(cents int64) error {
	if cents <= 0 || cents > MaxUnitPriceCents {
		return fmt.Errorf("%w: price is outside the allowed range", ErrValidation)
	}
	return nil
}

func ComputeLineCents(unitPriceCents, quantity int64) (int64, error) {
	if err := ValidatePriceCents(unitPriceCents); err != nil {
		return 0, err
	}
	if quantity <= 0 || quantity > MaxQuantity {
		return 0, fmt.Errorf("%w: quantity is outside the allowed range", ErrValidation)
	}
	if unitPriceCents > math.MaxInt64/quantity {
		return 0, fmt.Errorf("%w: sales amount overflow", ErrValidation)
	}
	return unitPriceCents * quantity, nil
}

func ComputeCommission(grossCents, commissionBPS int64) (int64, error) {
	if grossCents < 0 || commissionBPS < 0 || commissionBPS > MaxCommissionBPS {
		return 0, fmt.Errorf("%w: invalid commission input", ErrValidation)
	}
	if commissionBPS == 0 || grossCents == 0 {
		return 0, nil
	}
	if grossCents > math.MaxInt64/commissionBPS {
		return 0, fmt.Errorf("%w: commission calculation overflow", ErrValidation)
	}
	product := grossCents * commissionBPS
	commission := product / 10_000
	remainder := product % 10_000
	if remainder > 5_000 || (remainder == 5_000 && commission%2 != 0) {
		commission++
	}
	return commission, nil
}

func (c *Consignment) AddItem(rawSKU, itemName string, quantity, unitPriceCents int64) error {
	if c == nil {
		return fmt.Errorf("%w: consignment is nil", ErrValidation)
	}
	if c.State != StateDraft {
		return fmt.Errorf("%w: only draft consignments accept items", ErrStateConflict)
	}
	sku, displaySKU, err := NormalizeSKU(rawSKU)
	if err != nil {
		return err
	}
	name, err := cleanName(itemName, "item name")
	if err != nil {
		return err
	}
	if quantity <= 0 || quantity > MaxQuantity {
		return fmt.Errorf("%w: quantity is outside the allowed range", ErrValidation)
	}
	if err := ValidatePriceCents(unitPriceCents); err != nil {
		return err
	}
	if _, ok := c.itemBySKU(sku); ok {
		return fmt.Errorf("%w: sku already exists", ErrValidation)
	}
	c.Items = append(c.Items, ConsignmentItem{SKU: sku, DisplaySKU: displaySKU, Name: name, InitialQuantity: quantity, UnitPriceCents: unitPriceCents})
	return c.advanceVersion()
}

func (c *Consignment) UpdateDetails(sellerName string, commissionBPS int64) error {
	if c == nil {
		return fmt.Errorf("%w: consignment is nil", ErrValidation)
	}
	if c.State != StateDraft {
		return fmt.Errorf("%w: only draft consignments allow detail changes", ErrStateConflict)
	}
	seller, err := cleanName(sellerName, "seller name")
	if err != nil {
		return err
	}
	if commissionBPS < 0 || commissionBPS > MaxCommissionBPS {
		return fmt.Errorf("%w: commission must be between 0 and 10000", ErrValidation)
	}
	c.SellerName = seller
	c.CommissionBPS = commissionBPS
	return c.advanceVersion()
}

func (c *Consignment) UpdateItem(currentRawSKU, newRawSKU, itemName string, quantity, unitPriceCents int64) error {
	if c == nil {
		return fmt.Errorf("%w: consignment is nil", ErrValidation)
	}
	if c.State != StateDraft {
		return fmt.Errorf("%w: only draft consignments allow item changes", ErrStateConflict)
	}
	currentSKU, _, err := NormalizeSKU(currentRawSKU)
	if err != nil {
		return err
	}
	index, ok := c.itemBySKU(currentSKU)
	if !ok {
		return fmt.Errorf("%w: sku does not exist", ErrNotFound)
	}
	newSKU, displaySKU, err := NormalizeSKU(newRawSKU)
	if err != nil {
		return err
	}
	if other, exists := c.itemBySKU(newSKU); exists && other != index {
		return fmt.Errorf("%w: sku already exists", ErrValidation)
	}
	name, err := cleanName(itemName, "item name")
	if err != nil {
		return err
	}
	if quantity <= 0 || quantity > MaxQuantity {
		return fmt.Errorf("%w: quantity is outside the allowed range", ErrValidation)
	}
	if err := ValidatePriceCents(unitPriceCents); err != nil {
		return err
	}
	c.Items[index] = ConsignmentItem{
		SKU:             newSKU,
		DisplaySKU:      displaySKU,
		Name:            name,
		InitialQuantity: quantity,
		UnitPriceCents:  unitPriceCents,
	}
	return c.advanceVersion()
}

func (c *Consignment) RemoveItem(rawSKU string) error {
	if c == nil {
		return fmt.Errorf("%w: consignment is nil", ErrValidation)
	}
	if c.State != StateDraft {
		return fmt.Errorf("%w: only draft consignments allow item removal", ErrStateConflict)
	}
	sku, _, err := NormalizeSKU(rawSKU)
	if err != nil {
		return err
	}
	index, ok := c.itemBySKU(sku)
	if !ok {
		return fmt.Errorf("%w: sku does not exist", ErrNotFound)
	}
	copy(c.Items[index:], c.Items[index+1:])
	c.Items = c.Items[:len(c.Items)-1]
	return c.advanceVersion()
}

func (c *Consignment) Activate(at time.Time) error {
	if c == nil {
		return fmt.Errorf("%w: consignment is nil", ErrValidation)
	}
	if c.State != StateDraft {
		return fmt.Errorf("%w: consignment is not a draft", ErrStateConflict)
	}
	if len(c.Items) == 0 {
		return fmt.Errorf("%w: at least one item is required before activation", ErrValidation)
	}
	if at.IsZero() || at.Before(c.CreatedAt) {
		return fmt.Errorf("%w: activation time is invalid", ErrValidation)
	}
	c.State = StateActive
	c.ActivatedAt = at
	return c.advanceVersion()
}

func (c *Consignment) RecordSale(rawKey, rawSKU string, quantity int64, at time.Time) (SaleEvent, error) {
	if c == nil {
		return SaleEvent{}, fmt.Errorf("%w: consignment is nil", ErrValidation)
	}
	if c.State != StateActive {
		return SaleEvent{}, fmt.Errorf("%w: only active consignments accept sales", ErrStateConflict)
	}
	key, err := NormalizeIdempotencyKey(rawKey)
	if err != nil {
		return SaleEvent{}, err
	}
	sku, _, err := NormalizeSKU(rawSKU)
	if err != nil {
		return SaleEvent{}, err
	}
	if quantity <= 0 || quantity > MaxQuantity {
		return SaleEvent{}, fmt.Errorf("%w: quantity is outside the allowed range", ErrValidation)
	}
	if at.IsZero() || at.Before(c.ActivatedAt) {
		return SaleEvent{}, fmt.Errorf("%w: sale time is invalid", ErrValidation)
	}
	for _, sale := range c.Sales {
		if sale.IdempotencyKey == key {
			return SaleEvent{}, fmt.Errorf("%w: idempotency key already exists", ErrIdempotencyConflict)
		}
	}
	index, ok := c.itemBySKU(sku)
	if !ok {
		return SaleEvent{}, fmt.Errorf("%w: sku does not exist", ErrNotFound)
	}
	item := &c.Items[index]
	if quantity > item.InitialQuantity-item.SoldQuantity {
		return SaleEvent{}, ErrInsufficientStock
	}
	gross, err := ComputeLineCents(item.UnitPriceCents, quantity)
	if err != nil {
		return SaleEvent{}, err
	}
	if err := c.advanceVersion(); err != nil {
		return SaleEvent{}, err
	}
	item.SoldQuantity += quantity
	event := SaleEvent{IdempotencyKey: key, SKU: item.SKU, Quantity: quantity, GrossCents: gross, RecordedAt: at, ResultingVersion: c.Version}
	c.Sales = append(c.Sales, event)
	return event, nil
}

func (c *Consignment) BuildReceipt(at time.Time) (SettlementReceipt, error) {
	if c == nil {
		return SettlementReceipt{}, fmt.Errorf("%w: consignment is nil", ErrValidation)
	}
	if c.State != StateActive {
		return SettlementReceipt{}, fmt.Errorf("%w: only active consignments can be settled", ErrStateConflict)
	}
	if c.Receipt != nil {
		return SettlementReceipt{}, fmt.Errorf("%w: settlement receipt already exists", ErrStateConflict)
	}
	preview, err := c.PreviewSettlement(at)
	if err != nil {
		return SettlementReceipt{}, err
	}
	if err := c.advanceVersion(); err != nil {
		return SettlementReceipt{}, err
	}
	c.State = StateSettled
	c.SettledAt = at
	receipt := SettlementReceipt{ConsignmentID: c.ID, SellerName: c.SellerName, Lines: preview.Lines, GrossCents: preview.GrossCents, CommissionCents: preview.CommissionCents, SellerPayoutCents: preview.SellerPayoutCents, SettledAt: at, FinalVersion: c.Version}
	c.Receipt = &receipt
	return receipt, nil
}

func (c Consignment) FindSale(key string) (SaleEvent, bool) {
	if len(c.Sales) == 0 {
		return SaleEvent{}, false
	}
	latest := c.Sales[len(c.Sales)-1]
	if latest.IdempotencyKey != key {
		return SaleEvent{}, false
	}
	return latest, true
}

func (c Consignment) Validate() error {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.ID) != c.ID || len(c.ID) > 200 {
		return fmt.Errorf("%w: consignment id is required", ErrValidation)
	}
	seller, err := cleanName(c.SellerName, "seller name")
	if err != nil {
		return err
	}
	if seller != c.SellerName {
		return fmt.Errorf("%w: seller name is not normalized", ErrValidation)
	}
	if c.CommissionBPS < 0 || c.CommissionBPS > MaxCommissionBPS || c.Version <= 0 || c.CreatedAt.IsZero() {
		return fmt.Errorf("%w: consignment metadata is invalid", ErrValidation)
	}
	if c.State != StateDraft && c.State != StateActive && c.State != StateSettled {
		return fmt.Errorf("%w: unknown consignment state", ErrValidation)
	}
	if c.State != StateDraft && len(c.Items) == 0 {
		return fmt.Errorf("%w: active consignment must contain items", ErrValidation)
	}
	if c.State == StateDraft && (!c.ActivatedAt.IsZero() || !c.SettledAt.IsZero()) {
		return fmt.Errorf("%w: draft timestamps are invalid", ErrValidation)
	}
	if c.State == StateActive && (c.ActivatedAt.IsZero() || c.ActivatedAt.Before(c.CreatedAt) || !c.SettledAt.IsZero()) {
		return fmt.Errorf("%w: active timestamps are invalid", ErrValidation)
	}
	if c.State == StateSettled && (c.ActivatedAt.IsZero() || c.SettledAt.IsZero() || c.ActivatedAt.Before(c.CreatedAt) || c.SettledAt.Before(c.ActivatedAt)) {
		return fmt.Errorf("%w: settlement timestamps are invalid", ErrValidation)
	}
	seen := make(map[string]struct{}, len(c.Items))
	soldBySKU := make(map[string]int64, len(c.Items))
	for _, item := range c.Items {
		sku, display, err := NormalizeSKU(item.DisplaySKU)
		if err != nil || sku != item.SKU || display != item.DisplaySKU {
			return fmt.Errorf("%w: item sku is inconsistent", ErrValidation)
		}
		if _, exists := seen[item.SKU]; exists {
			return fmt.Errorf("%w: duplicate sku", ErrValidation)
		}
		seen[item.SKU] = struct{}{}
		soldBySKU[item.SKU] = 0
		cleanedName, nameErr := cleanName(item.Name, "item name")
		if nameErr != nil || cleanedName != item.Name || item.InitialQuantity <= 0 || item.InitialQuantity > MaxQuantity || item.SoldQuantity < 0 || item.SoldQuantity > item.InitialQuantity || ValidatePriceCents(item.UnitPriceCents) != nil {
			return fmt.Errorf("%w: item values are invalid", ErrValidation)
		}
	}
	keys := make(map[string]struct{}, len(c.Sales))
	lastSaleVersion := int64(0)
	for _, sale := range c.Sales {
		key, err := NormalizeIdempotencyKey(sale.IdempotencyKey)
		sku, _, skuErr := NormalizeSKU(sale.SKU)
		if err != nil || key != sale.IdempotencyKey || skuErr != nil || sku != sale.SKU || sale.Quantity <= 0 || sale.RecordedAt.IsZero() || sale.RecordedAt.Before(c.ActivatedAt) || sale.ResultingVersion <= lastSaleVersion || sale.ResultingVersion > c.Version {
			return fmt.Errorf("%w: sale event is invalid", ErrValidation)
		}
		if _, exists := keys[key]; exists {
			return fmt.Errorf("%w: duplicate idempotency key", ErrValidation)
		}
		keys[key] = struct{}{}
		if c.State == StateSettled && sale.RecordedAt.After(c.SettledAt) {
			return fmt.Errorf("%w: sale occurred after settlement", ErrValidation)
		}
		lastSaleVersion = sale.ResultingVersion
		itemIndex, ok := c.itemBySKU(sale.SKU)
		if !ok {
			return fmt.Errorf("%w: sale references unknown sku", ErrValidation)
		}
		item := c.Items[itemIndex]
		gross, grossErr := ComputeLineCents(item.UnitPriceCents, sale.Quantity)
		if grossErr != nil || gross != sale.GrossCents {
			return fmt.Errorf("%w: sale amount is inconsistent", ErrValidation)
		}
		if soldBySKU[sale.SKU] > math.MaxInt64-sale.Quantity {
			return fmt.Errorf("%w: sold quantity total overflow", ErrValidation)
		}
		soldBySKU[sale.SKU] += sale.Quantity
	}
	for _, item := range c.Items {
		if soldBySKU[item.SKU] != item.SoldQuantity {
			return fmt.Errorf("%w: item sold quantity does not match sale history", ErrValidation)
		}
	}
	if c.State == StateDraft && len(c.Sales) != 0 {
		return fmt.Errorf("%w: draft cannot contain sales", ErrValidation)
	}
	if c.State != StateSettled && c.Receipt != nil {
		return fmt.Errorf("%w: unsettled consignment has a receipt", ErrValidation)
	}
	if c.State == StateSettled {
		if c.Receipt == nil || c.Receipt.ConsignmentID != c.ID || c.Receipt.FinalVersion != c.Version {
			return fmt.Errorf("%w: settled consignment has no matching receipt", ErrValidation)
		}
		if err := c.Receipt.Validate(); err != nil {
			return err
		}
		expectedCommission, err := ComputeCommission(c.Receipt.GrossCents, c.CommissionBPS)
		if err != nil || expectedCommission != c.Receipt.CommissionCents || !c.Receipt.SettledAt.Equal(c.SettledAt) {
			return fmt.Errorf("%w: receipt settlement values do not match consignment", ErrValidation)
		}
		if c.Receipt.SellerName != c.SellerName || len(c.Receipt.Lines) != len(c.Items) {
			return fmt.Errorf("%w: receipt does not match consignment", ErrValidation)
		}
		for _, line := range c.Receipt.Lines {
			itemIndex, exists := c.itemBySKU(line.SKU)
			if !exists {
				return fmt.Errorf("%w: receipt line does not match item", ErrValidation)
			}
			item := c.Items[itemIndex]
			if line.SoldQuantity != item.SoldQuantity || line.ReturnedQuantity != item.InitialQuantity-item.SoldQuantity || line.UnitPriceCents != item.UnitPriceCents {
				return fmt.Errorf("%w: receipt line does not match item", ErrValidation)
			}
		}
	}
	return nil
}

func (r SettlementReceipt) Validate() error {
	seller, sellerErr := cleanName(r.SellerName, "seller name")
	if strings.TrimSpace(r.ConsignmentID) == "" || strings.TrimSpace(r.ConsignmentID) != r.ConsignmentID || sellerErr != nil || seller != r.SellerName || r.GrossCents < 0 || r.CommissionCents < 0 || r.SellerPayoutCents < 0 || r.SettledAt.IsZero() || r.FinalVersion <= 0 {
		return fmt.Errorf("%w: receipt metadata is invalid", ErrValidation)
	}
	var gross int64
	previousSKU := ""
	for _, line := range r.Lines {
		sku, display, skuErr := NormalizeSKU(line.DisplaySKU)
		if skuErr != nil || sku != line.SKU || display != line.DisplaySKU {
			return fmt.Errorf("%w: receipt sku is invalid", ErrValidation)
		}
		if line.SKU == previousSKU {
			return fmt.Errorf("%w: receipt contains duplicate sku", ErrValidation)
		}
		previousSKU = line.SKU
		if _, nameErr := cleanName(line.Name, "receipt item name"); nameErr != nil || line.SoldQuantity < 0 || line.ReturnedQuantity < 0 || line.UnitPriceCents <= 0 || line.SalesCents < 0 {
			return fmt.Errorf("%w: receipt line is invalid", ErrValidation)
		}
		if line.SoldQuantity > 0 {
			lineGross, err := ComputeLineCents(line.UnitPriceCents, line.SoldQuantity)
			if err != nil || lineGross != line.SalesCents {
				return fmt.Errorf("%w: receipt line amount is invalid", ErrValidation)
			}
		} else if line.SalesCents != 0 {
			return fmt.Errorf("%w: unsold receipt line has sales", ErrValidation)
		}
		if gross > math.MaxInt64-line.SalesCents {
			return fmt.Errorf("%w: receipt total overflow", ErrValidation)
		}
		gross += line.SalesCents
	}
	if gross != r.GrossCents || r.CommissionCents > r.GrossCents || r.SellerPayoutCents != r.GrossCents-r.CommissionCents {
		return fmt.Errorf("%w: receipt totals are inconsistent", ErrValidation)
	}
	return nil
}

func (s LedgerSnapshot) Validate() error {
	if s.FormatVersion != LedgerFormatVersion || s.Consignments == nil {
		return fmt.Errorf("%w: unsupported ledger format", ErrValidation)
	}
	for id, consignment := range s.Consignments {
		if id != consignment.ID {
			return fmt.Errorf("%w: ledger key does not match consignment id", ErrValidation)
		}
		if err := consignment.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c Consignment) Clone() Consignment {
	return c
}

func (s LedgerSnapshot) Clone() LedgerSnapshot {
	clone := s
	clone.Consignments = make(map[string]Consignment, len(s.Consignments))
	for id, consignment := range s.Consignments {
		clone.Consignments[id] = consignment.Clone()
	}
	return clone
}

func (c *Consignment) itemBySKU(sku string) (int, bool) {
	for index := range c.Items {
		if c.Items[index].SKU == sku {
			return index, true
		}
	}
	return 0, false
}

func (c *Consignment) advanceVersion() error {
	if c.Version == math.MaxInt64 {
		return fmt.Errorf("%w: version overflow", ErrValidation)
	}
	c.Version++
	return nil
}

func cleanName(raw, label string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 200 {
		return "", fmt.Errorf("%w: %s is required and must be short", ErrValidation, label)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: %s contains control characters", ErrValidation, label)
		}
	}
	return value, nil
}

func parseDigits(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("empty digits")
	}
	var result int64
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, errors.New("not a digit")
		}
		digit := int64(r - '0')
		if result > (math.MaxInt64-digit)/10 {
			return 0, errors.New("digit overflow")
		}
		result = result*10 + digit
	}
	return result, nil
}
