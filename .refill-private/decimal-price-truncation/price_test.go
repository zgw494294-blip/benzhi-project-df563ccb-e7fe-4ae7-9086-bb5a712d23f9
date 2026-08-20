package decimal_price_truncation_test

import (
	"testing"

	"stallsettle/internal/domain"
)

func TestDecimalPricePreservesExactCents(t *testing.T) {
	got, err := domain.ParsePriceCents("0.29")
	if err != nil {
		t.Fatalf("解析 0.29 失败：%v", err)
	}
	if got != 29 {
		t.Fatalf("0.29 应解析为 29 分，实际为 %d 分", got)
	}
}
