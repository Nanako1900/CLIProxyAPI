package management

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/accounting"
)

func (h *Handler) SetUsageAccounting(runtime *accounting.Runtime) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.usageAccounting = runtime
}

func (h *Handler) usageRuntime() *accounting.Runtime {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.usageAccounting
}

func usageJSON(c *gin.Context, fields gin.H) {
	c.Header("Cache-Control", "no-store")
	doc := ojson.NewDocString("{}")
	for name, value := range fields {
		if errSet := doc.Set(ojson.Escape(name), value); errSet != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot encode usage response"})
			return
		}
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", doc.Bytes())
}

func usageStatusJSON(c *gin.Context, status accounting.Status) {
	usageJSON(c, gin.H{"enabled": status.Enabled, "available": status.Available, "started_at": status.StartedAt, "pending_events": status.Pending, "last_error": status.LastError, "price_error": status.PriceError, "price_version": status.PriceVersion, "price_fetched_at": status.PriceFetchedAt, "price_source": status.PriceSource, "coverage": status.Coverage})
}

func usageAccountQuery(c *gin.Context) (accounting.Query, error) {
	q := accounting.Query{From: c.Query("from"), To: c.Query("to"), Model: c.Query("model"), Provider: c.Query("provider"), Sort: c.DefaultQuery("sort", "output_usd"), Limit: 50}
	for _, raw := range []string{q.From, q.To} {
		if raw != "" {
			if _, errParse := time.Parse("2006-01-02", raw); errParse != nil {
				return q, fmt.Errorf("from and to must use YYYY-MM-DD (inclusive UTC dates)")
			}
		}
	}
	if q.From != "" && q.To != "" && q.From > q.To {
		return q, fmt.Errorf("from must not be later than to")
	}
	switch q.Sort {
	case "output_usd", "total_usd", "output_tokens", "last_seen":
	default:
		return q, fmt.Errorf("unsupported sort field")
	}
	for _, param := range []struct {
		name   string
		target *int
		max    int
	}{{"offset", &q.Offset, 1000000}, {"limit", &q.Limit, 200}} {
		if raw := c.Query(param.name); raw != "" {
			n, errParse := strconv.Atoi(raw)
			if errParse != nil || n < 0 || n > param.max || (param.name == "limit" && n == 0) {
				return q, fmt.Errorf("invalid %s", param.name)
			}
			*param.target = n
		}
	}
	return q, nil
}

func (h *Handler) GetUsageAccountingStatus(c *gin.Context) {
	if runtime := h.usageRuntime(); runtime != nil {
		usageStatusJSON(c, runtime.Status())
		return
	}
	usageStatusJSON(c, accounting.Status{})
}

func (h *Handler) accountingStore(c *gin.Context) *accounting.Store {
	runtime := h.usageRuntime()
	if runtime != nil {
		if store := runtime.Store(); store != nil {
			return store
		}
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage accounting is unavailable; enable observability.usage.accounting"})
	return nil
}

func (h *Handler) GetUsageAccounts(c *gin.Context) {
	q, errQuery := usageAccountQuery(c)
	if errQuery != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errQuery.Error()})
		return
	}
	store := h.accountingStore(c)
	if store == nil {
		return
	}
	items, total, errList := store.Accounts(q)
	if errList != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot read usage accounts"})
		return
	}
	usageJSON(c, gin.H{"items": items, "total": total, "offset": q.Offset, "limit": q.Limit, "currency": "USD", "valuation": "API base-price estimate"})
}

func (h *Handler) GetUsageAccount(c *gin.Context) {
	q, errQuery := usageAccountQuery(c)
	if errQuery != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errQuery.Error()})
		return
	}
	store := h.accountingStore(c)
	if store == nil {
		return
	}
	item, errGet := store.Account(c.Param("id"), q)
	if errors.Is(errGet, accounting.ErrAccountNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": errGet.Error()})
		return
	}
	if errGet != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot read usage account"})
		return
	}
	usageJSON(c, gin.H{"account": item.Account, "daily": item.Daily, "models": item.Models})
}

func (h *Handler) GetUsageAccountRequests(c *gin.Context) {
	q, errQuery := usageAccountQuery(c)
	if errQuery != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errQuery.Error()})
		return
	}
	store := h.accountingStore(c)
	if store == nil {
		return
	}
	items, total, errList := store.Events(c.Param("id"), q)
	if errors.Is(errList, accounting.ErrAccountNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": errList.Error()})
		return
	}
	if errList != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot read usage events"})
		return
	}
	usageJSON(c, gin.H{"items": items, "total": total, "offset": q.Offset, "limit": q.Limit})
}

func (h *Handler) RefreshUsagePrices(c *gin.Context) {
	runtime := h.usageRuntime()
	if runtime == nil || runtime.Store() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage accounting unavailable"})
		return
	}
	if errRefresh := runtime.RefreshPrices(c.Request.Context()); errRefresh != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "price refresh failed; previous snapshot retained"})
		return
	}
	usageStatusJSON(c, runtime.Status())
}
