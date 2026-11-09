package accounting

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	"go.etcd.io/bbolt"
)

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	s, errOpen := Open(path, config.UsageAccountingConfig{Enabled: true})
	if errOpen != nil {
		t.Fatal(errOpen)
	}
	t.Cleanup(func() {
		if errClose := s.Close(); errClose != nil {
			t.Error(errClose)
		}
	})
	return s
}

func sampleRecord(id, auth string) usage.Record {
	return usage.Record{RequestID: id, AuthIndex: auth, AccountKey: auth, Provider: "openai", Model: "model", RequestedAt: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC), Detail: usage.Detail{Reported: true, InputTokens: 1000, OutputTokens: 200, ReasoningTokens: 50, TokenBreakdown: usage.NewSubsetTokenBreakdown(1000, 0, 0, 200, 50, 0)}}
}

func TestLedgerDurabilityDedupAndPrivacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	s := openTestStore(t, path)
	if errInstall := s.InstallPrices(pricePayload(t, map[string]string{"model": `tier("standard", p * 2 + c * 10)`}), "test"); errInstall != nil {
		t.Fatal(errInstall)
	}
	record := sampleRecord("execution-a", "auth-a")
	record.APIKey = "secret-client-key-never-store"
	record.AccessTokenSHA256 = "token-fingerprint-never-store"
	record.Source = "secret-upstream-key-never-store"
	for i := 0; i < 3; i++ {
		if errRecord := s.Record(context.Background(), record); errRecord != nil {
			t.Fatal(errRecord)
		}
	}
	if errProcess := s.ProcessPending(); errProcess != nil {
		t.Fatal(errProcess)
	}
	accounts, total, errList := s.Accounts(Query{})
	if errList != nil || total != 1 {
		t.Fatalf("accounts: %v %d", errList, total)
	}
	account := accounts[0]
	if account.Totals.Executions != 1 || account.Totals.OutputTokens != 200 {
		t.Fatal(account)
	}
	assertUSD(t, account.Totals.OutputUSD, "0.002")
	assertUSD(t, account.Totals.TotalUSD, "0.004")
	if errClose := s.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	data, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatal(errRead)
	}
	for _, secret := range []string{record.APIKey, record.AccessTokenSHA256, record.Source} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("ledger persisted secret material")
		}
	}
	reopened := openTestStore(t, path)
	if errRecord := reopened.Record(context.Background(), record); errRecord != nil {
		t.Fatal(errRecord)
	}
	if errProcess := reopened.ProcessPending(); errProcess != nil {
		t.Fatal(errProcess)
	}
	got, errGet := reopened.Account(account.ID, Query{})
	if errGet != nil || got.Account.Totals.Executions != 1 {
		t.Fatalf("restart duplicated event: %+v %v", got, errGet)
	}
}

func TestLedgerSnapshotsFiltersFailuresAndRetention(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	if errInstall := s.InstallPrices(pricePayload(t, map[string]string{"model": `tier("standard", p * 1 + c * 10)`}), "old"); errInstall != nil {
		t.Fatal(errInstall)
	}
	first := sampleRecord("one", "account")
	first.RequestedAt = first.RequestedAt.AddDate(0, 0, -1)
	if errRecord := s.Record(context.Background(), first); errRecord != nil {
		t.Fatal(errRecord)
	}
	if errInstall := s.InstallPrices(pricePayload(t, map[string]string{"model": `tier("standard", p * 1 + c * 20)`}), "new"); errInstall != nil {
		t.Fatal(errInstall)
	}
	second := sampleRecord("two", "account")
	second.Failed = true
	if errRecord := s.Record(context.Background(), second); errRecord != nil {
		t.Fatal(errRecord)
	}
	child := sampleRecord("child", "account")
	child.ParentExecutionID = "two"
	if errRecord := s.Record(context.Background(), child); errRecord != nil {
		t.Fatal(errRecord)
	}
	missing := sampleRecord("missing", "another-account")
	missing.Detail = usage.Detail{}
	if errRecord := s.Record(context.Background(), missing); errRecord != nil {
		t.Fatal(errRecord)
	}
	if errProcess := s.ProcessPending(); errProcess != nil {
		t.Fatal(errProcess)
	}
	accounts, total, errList := s.Accounts(Query{Sort: "output_usd"})
	if errList != nil || total != 2 {
		t.Fatal(errList, total)
	}
	a := accounts[0]
	assertUSD(t, a.Totals.OutputUSD, "0.010")
	if a.Totals.Executions != 2 || a.Totals.UsageEvents != 3 || a.Totals.Partial != 1 {
		t.Fatal(a)
	}
	filtered, _, errFilter := s.Accounts(Query{From: "2026-11-09", To: "2026-11-09", Model: "model", Sort: "output_usd"})
	if errFilter != nil {
		t.Fatal(errFilter)
	}
	assertUSD(t, filtered[0].Totals.OutputUSD, "0.008")
	events, n, errEvents := s.Events(a.ID, Query{Limit: 1})
	if errEvents != nil || n != 3 || len(events) != 1 {
		t.Fatal(errEvents, n, events)
	}
	s.mu.Lock()
	s.nowFunc = func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }
	s.mu.Unlock()
	if errPrune := s.Prune(); errPrune != nil {
		t.Fatal(errPrune)
	}
	_, n, errEvents = s.Events(a.ID, Query{})
	if errEvents != nil || n != 0 {
		t.Fatal(errEvents, n)
	}
	if errRecord := s.Record(context.Background(), first); errRecord != nil {
		t.Fatal(errRecord)
	}
	if errProcess := s.ProcessPending(); errProcess != nil {
		t.Fatal(errProcess)
	}
	got, errGet := s.Account(a.ID, Query{})
	if errGet != nil {
		t.Fatal(errGet)
	}
	assertUSD(t, got.Account.Totals.OutputUSD, "0.010")
}

func TestLedgerReplaysCommittedPendingEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	s := openTestStore(t, path)
	if errRecord := s.Record(context.Background(), sampleRecord("original", "account")); errRecord != nil {
		t.Fatal(errRecord)
	}
	if errProcess := s.ProcessPending(); errProcess != nil {
		t.Fatal(errProcess)
	}
	accounts, _, _ := s.Accounts(Query{})
	events, _, _ := s.Events(accounts[0].ID, Query{})
	event := events[0]
	event.ID = "committed-before-crash"
	event.Valuation.Status = "pending"
	if errClose := s.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	db, errOpen := bbolt.Open(path, 0600, nil)
	if errOpen != nil {
		t.Fatal(errOpen)
	}
	errUpdate := db.Update(func(tx *bbolt.Tx) error {
		if errPut := put(tx.Bucket([]byte("events")), event.ID, event); errPut != nil {
			return errPut
		}
		if errPut := tx.Bucket([]byte("dedup")).Put([]byte(event.ID), []byte{1}); errPut != nil {
			return errPut
		}
		if errPut := tx.Bucket([]byte("account-events")).Put([]byte(eventIndexKey(event)), []byte(event.ID)); errPut != nil {
			return errPut
		}
		return tx.Bucket([]byte("pending")).Put([]byte(event.ID), []byte{1})
	})
	if errUpdate != nil {
		t.Fatal(errUpdate)
	}
	if errClose := db.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	reopened := openTestStore(t, path)
	if errProcess := reopened.ProcessPending(); errProcess != nil {
		t.Fatal(errProcess)
	}
	if errRecord := reopened.Record(context.Background(), sampleRecord(event.ID, "account")); errRecord != nil {
		t.Fatal(errRecord)
	}
	if errProcess := reopened.ProcessPending(); errProcess != nil {
		t.Fatal(errProcess)
	}
	account, errGet := reopened.Account(event.AccountID, Query{})
	if errGet != nil || account.Account.Totals.Executions != 2 || reopened.Status().Pending != 0 {
		t.Fatal(account, errGet)
	}
}

func TestTotalsRejectOverflowWithoutChangingHistory(t *testing.T) {
	total := Totals{OutputTokens: math.MaxInt64, OutputUSD: "1.25"}
	before := total
	if errMerge := total.merge(Totals{Executions: 1, OutputTokens: 1, OutputUSD: "2"}); errMerge == nil {
		t.Fatal("token overflow accepted")
	}
	if total != before {
		t.Fatal("failed merge changed totals")
	}
}

func TestLedgerExplicitBindingsAndModelAliases(t *testing.T) {
	cfg := config.UsageAccountingConfig{Enabled: true, AccountBindings: map[string]string{"old-credential": "stable", "new-credential": "stable"}, ModelAliases: map[string]string{"upstream-alias": "gpt-4o"}}
	s, errOpen := Open(filepath.Join(t.TempDir(), "usage.db"), cfg)
	if errOpen != nil {
		t.Fatal(errOpen)
	}
	defer func() {
		if errClose := s.Close(); errClose != nil {
			t.Error(errClose)
		}
	}()
	for _, credential := range []string{"old-credential", "new-credential", "another-account"} {
		record := sampleRecord(credential, credential)
		record.UpstreamModel = "upstream-alias"
		if errRecord := s.Record(context.Background(), record); errRecord != nil {
			t.Fatal(errRecord)
		}
	}
	if errProcess := s.ProcessPending(); errProcess != nil {
		t.Fatal(errProcess)
	}
	accounts, n, errAccounts := s.Accounts(Query{Sort: "output_tokens"})
	if errAccounts != nil || n != 2 || accounts[0].Totals.Executions != 2 {
		t.Fatal(accounts, n, errAccounts)
	}
	events, n, errEvents := s.Events(accounts[0].ID, Query{})
	if errEvents != nil || n != 2 || events[0].PricingModel != "gpt-4o" || events[0].Valuation.Status != "priced" {
		t.Fatal(events, n, errEvents)
	}
}
