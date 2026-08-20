package duplicate_json_fields_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stallsettle/internal/application"
	"stallsettle/internal/domain"
	"stallsettle/internal/web"
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

func TestWriteRejectsDuplicateJSONFields(t *testing.T) {
	service, err := application.Open(context.Background(), &memoryLedger{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(web.NewHandler(service))
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/consignments", strings.NewReader(`{"seller_name":"甲摊主","seller_name":"乙摊主","commission_bps":0}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("重复 JSON 字段返回状态 %d，期望 %d", response.StatusCode, http.StatusBadRequest)
	}
}
