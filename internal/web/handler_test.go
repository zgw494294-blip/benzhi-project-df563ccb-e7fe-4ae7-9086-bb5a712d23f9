package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stallsettle/internal/application"
	"stallsettle/internal/domain"
)

type testLedger struct{ snapshot domain.LedgerSnapshot }

func (l *testLedger) Load(context.Context) (domain.LedgerSnapshot, error) {
	if l.snapshot.Consignments == nil {
		return domain.NewEmptySnapshot(), nil
	}
	return l.snapshot.Clone(), nil
}

func (l *testLedger) Save(_ context.Context, snapshot domain.LedgerSnapshot) error {
	l.snapshot = snapshot.Clone()
	return nil
}

func TestHandlerRejectsUnknownFieldsAndInvalidPrices(t *testing.T) {
	ledger := &testLedger{}
	service, err := application.Open(context.Background(), ledger, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(service))
	defer server.Close()
	response := post(t, server.URL+"/api/consignments", `{"seller_name":"摊主","commission_bps":100,"extra":true}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", response.StatusCode)
	}
	created := post(t, server.URL+"/api/consignments", `{"seller_name":"摊主","commission_bps":100}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", created.StatusCode)
	}
	var payload struct {
		Consignment domain.Consignment `json:"consignment"`
	}
	if err := json.NewDecoder(created.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	created.Body.Close()
	response = post(t, server.URL+"/api/consignments/"+payload.Consignment.ID+"/items", `{"version":1,"sku":"SKU","name":"商品","quantity":1,"unit_price":"1e2"}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid price status = %d", response.StatusCode)
	}
}

func TestHandlerServesWorkspaceAssets(t *testing.T) {
	ledger := &testLedger{}
	service, err := application.Open(context.Background(), ledger, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(service))
	defer server.Close()
	for _, path := range []string{"/", "/assets/app.js", "/assets/style.css"} {
		response, requestErr := http.Get(server.URL + path)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, response.StatusCode)
		}
		response.Body.Close()
	}
}

func TestHandlerSupportsDraftMaintenanceAndQueryStatistics(t *testing.T) {
	ledger := &testLedger{}
	service, err := application.Open(context.Background(), ledger, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(service))
	defer server.Close()

	var created struct {
		Consignment domain.Consignment `json:"consignment"`
	}
	decodeResponse(t, doJSON(t, http.MethodPost, server.URL+"/api/consignments", `{"seller_name":"旧名称","commission_bps":500}`), http.StatusCreated, &created)
	decodeResponse(t, doJSON(t, http.MethodPut, server.URL+"/api/consignments/"+created.Consignment.ID, `{"version":1,"seller_name":"青山陶作","commission_bps":1000}`), http.StatusOK, &created)
	decodeResponse(t, doJSON(t, http.MethodPost, server.URL+"/api/consignments/"+created.Consignment.ID+"/items", `{"version":2,"sku":"CUP","name":"陶杯","quantity":4,"unit_price":"5.00"}`), http.StatusOK, &created)
	decodeResponse(t, doJSON(t, http.MethodPost, server.URL+"/api/consignments/"+created.Consignment.ID+"/items", `{"version":3,"sku":"DROP","name":"待删除","quantity":1,"unit_price":"1.00"}`), http.StatusOK, &created)
	decodeResponse(t, doJSON(t, http.MethodPut, server.URL+"/api/consignments/"+created.Consignment.ID+"/items/CUP", `{"version":4,"sku":"CUP-01","name":"釉面陶杯","quantity":5,"unit_price":"6.00"}`), http.StatusOK, &created)
	decodeResponse(t, doJSON(t, http.MethodDelete, server.URL+"/api/consignments/"+created.Consignment.ID+"/items/DROP", `{"version":5}`), http.StatusOK, &created)
	if created.Consignment.Version != 6 || created.Consignment.SellerName != "青山陶作" || len(created.Consignment.Items) != 1 || created.Consignment.Items[0].SKU != "CUP-01" {
		t.Fatalf("unexpected maintained draft: %#v", created.Consignment)
	}

	var detail application.ConsignmentDetail
	decodeResponse(t, doJSON(t, http.MethodGet, server.URL+"/api/consignments/"+created.Consignment.ID, ""), http.StatusOK, &detail)
	if detail.Summary.InitialQuantity != 5 || detail.Summary.InitialRetailCents != 3000 || len(detail.Inventory) != 1 {
		t.Fatalf("unexpected detail response: %#v", detail)
	}
	var result application.ListResult
	decodeResponse(t, doJSON(t, http.MethodGet, server.URL+"/api/consignments?state=draft&q=%E9%87%89%E9%9D%A2&sort=seller", ""), http.StatusOK, &result)
	if result.Matched != 1 || result.Overview.DraftConsignments != 1 || result.Overview.ItemKinds != 1 {
		t.Fatalf("unexpected list response: %#v", result)
	}
}

func TestHandlerRequiresJSONContentTypeForWrites(t *testing.T) {
	ledger := &testLedger{}
	service, err := application.Open(context.Background(), ledger, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(service))
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/consignments", strings.NewReader(`{"seller_name":"摊主","commission_bps":100}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "text/plain")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("plain text write status = %d", response.StatusCode)
	}
}

func post(t *testing.T, endpoint, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func doJSON(t *testing.T, method, endpoint, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, status int, target any) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != status {
		t.Fatalf("response status = %d, want %d", response.StatusCode, status)
	}
	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
}
