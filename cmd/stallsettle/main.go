package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"stallsettle/internal/application"
	"stallsettle/internal/domain"
	"stallsettle/internal/fileledger"
	"stallsettle/internal/web"
)

func main() {
	address := flag.String("addr", ":8080", "服务监听地址")
	ledgerPath := flag.String("ledger", "stallsettle.json", "本地账本路径")
	smoke := flag.Bool("smoke", false, "执行完整流程自检")
	flag.Parse()
	if *smoke {
		if err := runSmoke(); err != nil {
			fmt.Fprintf(os.Stderr, "自检失败：%v\n", err)
			os.Exit(1)
		}
		fmt.Println("StallSettle 自检通过：建档修改、商品维护、上架、销售、幂等重放、结算统计和重载均正常。")
		return
	}
	if err := runServer(*address, *ledgerPath); err != nil {
		fmt.Fprintf(os.Stderr, "服务停止：%v\n", err)
		os.Exit(1)
	}
}

func runServer(address, ledgerPath string) error {
	ledger, err := fileledger.New(ledgerPath)
	if err != nil {
		return err
	}
	app, err := application.Open(context.Background(), ledger, application.RealClock{}, &application.SequentialIDGenerator{})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: address, Handler: web.NewHandler(app), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signalContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		return nil
	}
}

func runSmoke() error {
	temporaryDirectory, err := os.MkdirTemp("", "stallsettle-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporaryDirectory)
	ledgerPath := filepath.Join(temporaryDirectory, "ledger.json")
	ledger, err := fileledger.New(ledgerPath)
	if err != nil {
		return err
	}
	app, err := application.Open(context.Background(), ledger, application.RealClock{}, &application.SequentialIDGenerator{})
	if err != nil {
		return err
	}
	server := httptest.NewServer(web.NewHandler(app))
	defer server.Close()
	client := server.Client()

	var created struct {
		Consignment domain.Consignment `json:"consignment"`
	}
	if err := callJSON(client, http.MethodPost, server.URL+"/api/consignments", `{"seller_name":"南风陶坊","commission_bps":1000}`, http.StatusCreated, &created); err != nil {
		return err
	}
	if err := callJSON(client, http.MethodPut, server.URL+"/api/consignments/"+created.Consignment.ID, fmt.Sprintf(`{"version":%d,"seller_name":"南风陶坊","commission_bps":1000}`, created.Consignment.Version), http.StatusOK, &created); err != nil {
		return err
	}
	if err := callJSON(client, http.MethodPost, server.URL+"/api/consignments/"+created.Consignment.ID+"/items", fmt.Sprintf(`{"version":%d,"sku":" A-01 ","name":"手工陶杯","quantity":5,"unit_price":"12.80"}`, created.Consignment.Version), http.StatusOK, &created); err != nil {
		return err
	}
	if err := callJSON(client, http.MethodPost, server.URL+"/api/consignments/"+created.Consignment.ID+"/items", fmt.Sprintf(`{"version":%d,"sku":"TEMP","name":"临时商品","quantity":1,"unit_price":"1.00"}`, created.Consignment.Version), http.StatusOK, &created); err != nil {
		return err
	}
	if err := callJSON(client, http.MethodDelete, server.URL+"/api/consignments/"+created.Consignment.ID+"/items/TEMP", fmt.Sprintf(`{"version":%d}`, created.Consignment.Version), http.StatusOK, &created); err != nil {
		return err
	}
	if err := callJSON(client, http.MethodPut, server.URL+"/api/consignments/"+created.Consignment.ID+"/items/A-01", fmt.Sprintf(`{"version":%d,"sku":"A-01","name":"手工陶杯","quantity":5,"unit_price":"12.80"}`, created.Consignment.Version), http.StatusOK, &created); err != nil {
		return err
	}
	if err := callJSON(client, http.MethodPost, server.URL+"/api/consignments/"+created.Consignment.ID+"/activate", fmt.Sprintf(`{"version":%d}`, created.Consignment.Version), http.StatusOK, &created); err != nil {
		return err
	}
	var saleResponse struct {
		Consignment domain.Consignment `json:"consignment"`
		Sale        domain.SaleEvent   `json:"sale"`
	}
	if err := callJSON(client, http.MethodPost, server.URL+"/api/consignments/"+created.Consignment.ID+"/sales", fmt.Sprintf(`{"version":%d,"idempotency_key":"smoke-sale-1","sku":"a-01","quantity":2}`, created.Consignment.Version), http.StatusOK, &saleResponse); err != nil {
		return err
	}
	var replay struct {
		Consignment domain.Consignment `json:"consignment"`
		Sale        domain.SaleEvent   `json:"sale"`
	}
	if err := callJSON(client, http.MethodPost, server.URL+"/api/consignments/"+created.Consignment.ID+"/sales", fmt.Sprintf(`{"version":%d,"idempotency_key":"smoke-sale-1","sku":"A-01","quantity":2}`, created.Consignment.Version-1), http.StatusOK, &replay); err != nil {
		return err
	}
	if len(replay.Consignment.Sales) != 1 || replay.Consignment.Items[0].SoldQuantity != 2 || replay.Sale.ResultingVersion != saleResponse.Sale.ResultingVersion {
		return errors.New("幂等重放改变了销售结果")
	}
	var settled struct {
		Consignment domain.Consignment       `json:"consignment"`
		Receipt     domain.SettlementReceipt `json:"receipt"`
	}
	if err := callJSON(client, http.MethodPost, server.URL+"/api/consignments/"+created.Consignment.ID+"/settle", fmt.Sprintf(`{"version":%d}`, replay.Consignment.Version), http.StatusOK, &settled); err != nil {
		return err
	}
	if settled.Receipt.GrossCents != 2560 || settled.Receipt.CommissionCents != 256 || settled.Receipt.SellerPayoutCents != 2304 {
		return errors.New("结算金额不正确")
	}
	var listed application.ListResult
	if err := callJSON(client, http.MethodGet, server.URL+"/api/consignments?state=settled&q=A-01&sort=newest", "", http.StatusOK, &listed); err != nil {
		return err
	}
	if listed.Matched != 1 || listed.Overview.SettledConsignments != 1 || listed.Overview.GrossSalesCents != 2560 {
		return errors.New("账本查询统计不正确")
	}
	server.Close()
	reloadedLedger, err := fileledger.New(ledgerPath)
	if err != nil {
		return err
	}
	reloaded, err := application.Open(context.Background(), reloadedLedger, application.RealClock{}, &application.SequentialIDGenerator{})
	if err != nil {
		return err
	}
	reloadedConsignment, err := reloaded.Get(context.Background(), created.Consignment.ID)
	if err != nil {
		return err
	}
	if reloadedConsignment.State != domain.StateSettled || reloadedConsignment.Receipt == nil || reloadedConsignment.Receipt.SellerPayoutCents != 2304 {
		return errors.New("重载后的结算凭据不完整")
	}
	return nil
}

func callJSON(client *http.Client, method, endpoint, body string, expectedStatus int, target any) error {
	request, err := http.NewRequest(method, endpoint, strings.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		payload, _ := io.ReadAll(response.Body)
		return fmt.Errorf("请求 %s 返回 %d：%s", endpoint, response.StatusCode, string(payload))
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(target)
}
