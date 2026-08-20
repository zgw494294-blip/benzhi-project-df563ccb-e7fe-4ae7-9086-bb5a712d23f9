package version_overflow_mutation_test

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"stallsettle/internal/domain"
)

func TestVersionOverflowLeavesDraftUnchanged(t *testing.T) {
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		change func(*domain.Consignment) error
	}{
		{"添加商品", func(c *domain.Consignment) error { return c.AddItem("C", "商品 C", 1, 100) }},
		{"修改详情", func(c *domain.Consignment) error { return c.UpdateDetails("新摊主", 500) }},
		{"修改商品", func(c *domain.Consignment) error { return c.UpdateItem("A", "C", "新商品", 2, 200) }},
		{"删除商品", func(c *domain.Consignment) error { return c.RemoveItem("A") }},
		{"上架", func(c *domain.Consignment) error { return c.Activate(now) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			consignment, err := domain.NewConsignment("csn-version-overflow", "摊主", 0, now)
			if err != nil {
				t.Fatal(err)
			}
			if err := consignment.AddItem("A", "商品 A", 2, 100); err != nil {
				t.Fatal(err)
			}
			if err := consignment.AddItem("B", "商品 B", 2, 100); err != nil {
				t.Fatal(err)
			}
			consignment.Version = math.MaxInt64
			before := marshal(t, consignment)
			if err := test.change(&consignment); err == nil {
				t.Fatal("版本溢出时操作意外成功")
			}
			after := marshal(t, consignment)
			if before != after {
				t.Fatalf("操作失败后寄售单发生变化\n前：%s\n后：%s", before, after)
			}
		})
	}
}

func marshal(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}
