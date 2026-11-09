package accounting

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/home"
)

func TestRuntimeRefreshAndDisabledHistory(t *testing.T) {
	payload := pricePayload(t, map[string]string{"model": `tier("standard", p * 1 + c * 2)`})
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	cfg := &config.Config{AuthDir: t.TempDir(), UsageAccounting: config.UsageAccountingConfig{Enabled: true, Pricing: config.UsagePricingConfig{SourceURL: server.URL}}}
	runtime := NewRuntime(cfg)
	defer func() {
		if errClose := runtime.Close(); errClose != nil {
			t.Error(errClose)
		}
	}()
	if errRefresh := runtime.RefreshPrices(context.Background()); errRefresh != nil {
		t.Fatal(errRefresh)
	}
	version := runtime.Status().PriceVersion
	fail.Store(true)
	if errRefresh := runtime.RefreshPrices(context.Background()); errRefresh == nil {
		t.Fatal("failed refresh accepted")
	}
	if runtime.Status().PriceVersion != version || runtime.Status().PriceError == "" {
		t.Fatal("failed refresh discarded snapshot")
	}
	if errRecord := runtime.Record(context.Background(), sampleRecord("one", "account")); errRecord != nil {
		t.Fatal(errRecord)
	}
	if errProcess := runtime.Store().ProcessPending(); errProcess != nil {
		t.Fatal(errProcess)
	}
	cfg.UsageAccounting.Enabled = false
	runtime.Configure(cfg)
	if runtime.Status().Enabled || !runtime.Status().Available {
		t.Fatal("disabling collection lost readable history")
	}
	if errRecord := runtime.Record(context.Background(), sampleRecord("two", "account")); errRecord != nil {
		t.Fatal(errRecord)
	}
	accounts, _, errList := runtime.Store().Accounts(Query{})
	if errList != nil || accounts[0].Totals.Executions != 1 {
		t.Fatal(accounts, errList)
	}
	cfg.UsageAccounting.Path = filepath.Join(t.TempDir(), "other.db")
	runtime.Configure(cfg)
	if runtime.Status().LastError == "" {
		t.Fatal("live path change silently ignored")
	}
}

func TestRuntimeCloseCancelsAdministrativeRefresh(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer server.Close()
	runtime := NewRuntime(&config.Config{AuthDir: t.TempDir(), UsageAccounting: config.UsageAccountingConfig{Enabled: true, Pricing: config.UsagePricingConfig{SourceURL: server.URL}}})
	done := make(chan error, 1)
	go func() { done <- runtime.RefreshPrices(context.Background()) }()
	<-started
	if errClose := runtime.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	if errRefresh := <-done; errRefresh == nil {
		t.Fatal("shutdown did not cancel the refresh")
	}
}

func TestRuntimeHomeDoesNotOpenLocalLedger(t *testing.T) {
	previous := home.Current()
	t.Cleanup(func() { home.SetCurrent(previous) })
	for _, activeClient := range []bool{false, true} {
		cfg := &config.Config{AuthDir: t.TempDir(), Home: config.HomeConfig{Enabled: !activeClient}, UsageAccounting: config.UsageAccountingConfig{Enabled: true}}
		if activeClient {
			home.SetCurrent(&home.Client{})
		}
		runtime := NewRuntime(cfg)
		if runtime.Store() != nil || runtime.Status().Enabled {
			t.Fatal("Home mode opened local accounting")
		}
		if errClose := runtime.Close(); errClose != nil {
			t.Fatal(errClose)
		}
		if _, errStat := os.Stat(filepath.Join(cfg.AuthDir, "usage.db")); !os.IsNotExist(errStat) {
			t.Fatal("Home mode created local ledger", errStat)
		}
	}
}
