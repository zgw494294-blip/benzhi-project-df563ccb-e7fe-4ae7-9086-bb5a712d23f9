package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"stallsettle/internal/application"
	"stallsettle/internal/domain"
)

//go:embed assets/*
var assets embed.FS

const maxRequestBytes int64 = 64 << 10

type Handler struct {
	app *application.Service
}

func NewHandler(app *application.Service) http.Handler {
	handler := &Handler{app: app}
	mux := http.NewServeMux()
	mux.HandleFunc("/", handler.handlePage)
	mux.Handle("/assets/", http.FileServer(http.FS(assets)))
	mux.HandleFunc("/api/consignments", handler.handleConsignments)
	mux.HandleFunc("/api/consignments/", handler.handleConsignment)
	return withResponseHeaders(mux)
}

type createRequest struct {
	SellerName    string `json:"seller_name"`
	CommissionBPS int64  `json:"commission_bps"`
}

type updateConsignmentRequest struct {
	Version       int64  `json:"version"`
	SellerName    string `json:"seller_name"`
	CommissionBPS int64  `json:"commission_bps"`
}

type itemRequest struct {
	Version   int64  `json:"version"`
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Quantity  int64  `json:"quantity"`
	UnitPrice string `json:"unit_price"`
}

type updateItemRequest struct {
	Version   int64  `json:"version"`
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Quantity  int64  `json:"quantity"`
	UnitPrice string `json:"unit_price"`
}

type versionRequest struct {
	Version int64 `json:"version"`
}

type saleRequest struct {
	Version        int64  `json:"version"`
	IdempotencyKey string `json:"idempotency_key"`
	SKU            string `json:"sku"`
	Quantity       int64  `json:"quantity"`
}

func (h *Handler) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	data, err := assets.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, "页面暂时不可用", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (h *Handler) handleConsignments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		result, err := h.app.QueryConsignments(r.Context(), application.ListOptions{
			State: domain.ConsignmentState(r.URL.Query().Get("state")),
			Query: r.URL.Query().Get("q"),
			Sort:  r.URL.Query().Get("sort"),
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPost:
		var input createRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, err)
			return
		}
		consignment, err := h.app.CreateConsignment(r.Context(), input.SellerName, input.CommissionBPS)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"consignment": consignment})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (h *Handler) handleConsignment(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/consignments/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			detail, getErr := h.app.GetDetail(r.Context(), id)
			if getErr != nil {
				writeError(w, getErr)
				return
			}
			writeJSON(w, http.StatusOK, detail)
		case http.MethodPut:
			var input updateConsignmentRequest
			if err := decodeJSON(w, r, &input); err != nil {
				writeError(w, err)
				return
			}
			consignment, updateErr := h.app.UpdateConsignment(r.Context(), id, input.Version, input.SellerName, input.CommissionBPS)
			if updateErr != nil {
				writeError(w, updateErr)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"consignment": consignment})
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPut)
		}
		return
	}
	if len(parts) == 3 && parts[1] == "items" {
		h.handleItemResource(w, r, id, parts[2])
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	switch parts[1] {
	case "items":
		var input itemRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, err)
			return
		}
		price, parseErr := domain.ParsePriceCents(input.UnitPrice)
		if parseErr != nil {
			writeError(w, parseErr)
			return
		}
		consignment, appErr := h.app.AddItem(r.Context(), id, input.Version, input.Quantity, price, input.SKU, input.Name)
		if appErr != nil {
			writeError(w, appErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"consignment": consignment})
	case "activate":
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, err)
			return
		}
		consignment, appErr := h.app.Activate(r.Context(), id, input.Version)
		if appErr != nil {
			writeError(w, appErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"consignment": consignment})
	case "sales":
		var input saleRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, err)
			return
		}
		consignment, event, appErr := h.app.RecordSale(r.Context(), id, input.Version, input.IdempotencyKey, input.SKU, input.Quantity)
		if appErr != nil {
			writeError(w, appErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"consignment": consignment, "sale": event})
	case "settle":
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, err)
			return
		}
		consignment, receipt, appErr := h.app.Settle(r.Context(), id, input.Version)
		if appErr != nil {
			writeError(w, appErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"consignment": consignment, "receipt": receipt})
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleItemResource(w http.ResponseWriter, r *http.Request, id, escapedSKU string) {
	currentSKU, err := url.PathUnescape(escapedSKU)
	if err != nil || currentSKU == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var input updateItemRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, err)
			return
		}
		price, err := domain.ParsePriceCents(input.UnitPrice)
		if err != nil {
			writeError(w, err)
			return
		}
		consignment, err := h.app.UpdateItem(r.Context(), id, input.Version, input.Quantity, price, currentSKU, input.SKU, input.Name)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"consignment": consignment})
	case http.MethodDelete:
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, err)
			return
		}
		consignment, err := h.app.RemoveItem(r.Context(), id, input.Version, currentSKU)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"consignment": consignment})
	default:
		methodNotAllowed(w, http.MethodPut, http.MethodDelete)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	contentType := r.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return errors.New("请求内容类型必须是 application/json")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("请求内容不能为空")
		}
		return errors.New("请求内容格式不正确")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("请求内容只能包含一个 JSON 对象")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	status, message := classifyError(err)
	writeJSON(w, status, map[string]any{"error": message})
}

func classifyError(err error) (int, string) {
	switch {
	case errors.Is(err, application.ErrStorage):
		return http.StatusInternalServerError, "账本保存失败，请稍后重试"
	case errors.Is(err, application.ErrVersionConflict):
		return http.StatusConflict, "页面数据已更新，请刷新后重试"
	case errors.Is(err, domain.ErrIdempotencyConflict):
		return http.StatusConflict, "幂等键已对应另一笔销售"
	case errors.Is(err, domain.ErrStateConflict):
		return http.StatusConflict, "当前状态不允许执行此操作"
	case errors.Is(err, domain.ErrInsufficientStock):
		return http.StatusConflict, "可售数量不足"
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "没有找到这份寄售单或商品"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout, "请求已取消"
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest, validationMessage(err)
	default:
		return http.StatusBadRequest, "请求无法处理"
	}
}

func validationMessage(err error) string {
	message := err.Error()
	if strings.Contains(message, "price") {
		return "单价格式或范围不正确"
	}
	if strings.Contains(message, "sku") {
		return "商品编号格式不正确或已存在"
	}
	if strings.Contains(message, "quantity") {
		return "数量必须是有效的正整数"
	}
	return "输入内容不符合要求"
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "请求方法不受支持"})
}

func withResponseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
