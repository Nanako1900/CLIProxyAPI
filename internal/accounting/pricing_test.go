package accounting

import (
	"math/big"
	"testing"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func pricePayload(t *testing.T, expressions map[string]string) []byte {
	t.Helper()
	doc := ojson.NewDocString(`{"success":true,"data":{}}`)
	if errSet := doc.Set("data.billing_expr", expressions); errSet != nil {
		t.Fatal(errSet)
	}
	return doc.Bytes()
}

func assertUSD(t *testing.T, got, want string) {
	t.Helper()
	a, okA := new(big.Rat).SetString(got)
	b, okB := new(big.Rat).SetString(want)
	if !okA || !okB || a.Cmp(b) != 0 {
		t.Fatalf("USD got %q, want %q", got, want)
	}
}

func TestPricingCanonicalBuckets(t *testing.T) {
	snapshot, rules, errParse := parsePrices(pricePayload(t, map[string]string{"model": `tier("standard", p * 3 + cr * 0.3 + cc * 3.75 + cc1h * 6 + c * 15)`}), "test", time.Now())
	if errParse != nil {
		t.Fatal(errParse)
	}
	event := Event{UsageReported: true, Model: "model", Tokens: usage.NewIndependentTokenBreakdown(1000, 500, 300, 150, 50, 0), Cache1h: 100, RequestedAt: time.Now()}
	value := priceEvent(event, snapshot, rules)
	if value.Status != "priced" {
		t.Fatalf("valuation: %+v", value)
	}
	assertUSD(t, value.OutputUSD, "0.003")
	assertUSD(t, value.TotalUSD, "0.0075")
	if value.Tier != "standard" {
		t.Fatal(value)
	}
}

func TestPricingConditionsKeepInputContextForOutput(t *testing.T) {
	payload := pricePayload(t, map[string]string{"tiered": `len <= 200000 ? tier("short", p * 2 + cr * 0.2 + c * 10) : tier("long", p * 4 + cr * 0.4 + c * 15)`, "thinking": `param("enable_thinking") == true ? tier("thinking", p * 1 + c * 4) : tier("standard", p * 1 + c * 2)`, "schedule": `weekday("UTC") >= 1 && weekday("UTC") <= 5 && hour("UTC") >= 1 && hour("UTC") < 4 ? tier("peak", p * 1 + c * 6) : tier("off_peak", p * 0.5 + c * 3)`})
	snapshot, rules, errParse := parsePrices(payload, "test", time.Now())
	if errParse != nil {
		t.Fatal(errParse)
	}
	for _, tc := range []struct {
		model      string
		input      int64
		params     map[string]bool
		hour       int
		want, tier string
	}{{"tiered", 200000, nil, 2, "0.01", "short"}, {"tiered", 200001, nil, 2, "0.015", "long"}, {"thinking", 10, map[string]bool{"enable_thinking": true}, 2, "0.004", "thinking"}, {"thinking", 10, map[string]bool{"enable_thinking": false}, 2, "0.002", "standard"}, {"schedule", 10, nil, 2, "0.006", "peak"}, {"schedule", 10, nil, 12, "0.003", "off_peak"}} {
		t.Run(tc.model+tc.tier, func(t *testing.T) {
			e := Event{UsageReported: true, Model: tc.model, Tokens: usage.NewSubsetTokenBreakdown(tc.input, 0, 0, 1000, 100, 0), PricingParams: tc.params, RequestedAt: time.Date(2026, 11, 9, tc.hour, 0, 0, 0, time.UTC)}
			v := priceEvent(e, snapshot, rules)
			if v.Status != "priced" || v.Tier != tc.tier {
				t.Fatal(v)
			}
			assertUSD(t, v.OutputUSD, tc.want)
		})
	}
	missing := priceEvent(Event{UsageReported: true, Model: "thinking", Tokens: usage.NewSubsetTokenBreakdown(10, 0, 0, 10, 0, 0)}, snapshot, rules)
	if missing.Status != "unpriced" || missing.Reason != "invalid_pricing_context" {
		t.Fatal(missing)
	}
}

func TestPricingGapsZeroAndFailedUsage(t *testing.T) {
	snapshot, rules, errParse := parsePrices(pricePayload(t, map[string]string{"model": `tier("standard", p * 0.00547945 + c * 0.0273973)`}), "test", time.Now())
	if errParse != nil {
		t.Fatal(errParse)
	}
	e := Event{UsageReported: true, Model: "model", Tokens: usage.NewSubsetTokenBreakdown(1, 0, 0, 1, 0, 0)}
	v := priceEvent(e, snapshot, rules)
	assertUSD(t, v.TotalUSD, "0.00000003287675")
	e.Failed = true
	v = priceEvent(e, snapshot, rules)
	if v.Status != "partial" {
		t.Fatal(v)
	}
	e.Failed = false
	e.Tokens = usage.NewSubsetTokenBreakdown(0, 0, 0, 0, 0, 0)
	v = priceEvent(e, snapshot, rules)
	if v.Status != "priced" {
		t.Fatal(v)
	}
	assertUSD(t, v.TotalUSD, "0")
	e.UsageReported = false
	v = priceEvent(e, snapshot, rules)
	if v.Reason != "missing_usage" {
		t.Fatal(v)
	}
	e.UsageReported = true
	e.Model = "unknown"
	v = priceEvent(e, snapshot, rules)
	if v.Reason != "unknown_model" {
		t.Fatal(v)
	}
}

func TestBuiltinPricesCompile(t *testing.T) {
	_, rules, errParse := parsePrices(builtinPrices, DefaultPriceURL, time.Now())
	if errParse != nil {
		t.Fatal(errParse)
	}
	if len(rules) < 100 {
		t.Fatal("incomplete built-in catalog")
	}
	for model, rule := range rules {
		if rule.err != nil {
			t.Errorf("%s: %v", model, rule.err)
		}
	}
}

func TestInvalidExpressionsFailWithoutPanics(t *testing.T) {
	for _, source := range []string{"", `tier("x")`, `unknown()`, `p ** c`, `p ? c`, `param("x",1)`, `len <= 100 ? 1 :`, `(p + c`, `arbitrary`, `tier("x", p + c);`} {
		if _, _, errCompile := compileExpression(source); errCompile == nil {
			t.Errorf("accepted %q", source)
		}
	}
}

func TestPricingDoesNotAssignFixedFeesToOutputTokens(t *testing.T) {
	snapshot, rules, errParse := parsePrices(pricePayload(t, map[string]string{"model": `tier("standard", fixed(0.01) + p * 2 + c * 10)`, "cross-bucket": `p * c`}), "test", time.Now())
	if errParse != nil {
		t.Fatal(errParse)
	}
	event := Event{Model: "model", UsageReported: true, Tokens: usage.NewSubsetTokenBreakdown(1000, 0, 0, 200, 0, 0)}
	value := priceEvent(event, snapshot, rules)
	if value.Status != "priced" {
		t.Fatal(value)
	}
	assertUSD(t, value.OutputUSD, "0.002")
	assertUSD(t, value.TotalUSD, "0.014")
	event.Model = "cross-bucket"
	if value = priceEvent(event, snapshot, rules); value.Status != "unpriced" {
		t.Fatal("priced an ambiguous output contribution", value)
	}
}
