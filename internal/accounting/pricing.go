package accounting

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

const DefaultPriceURL = "https://basellm.github.io/llm-metadata/api/newapi/ratio_config-v1-base.json"

//go:embed prices.json
var builtinPrices []byte

type PriceSnapshot struct {
	Version     string            `json:"version"`
	Source      string            `json:"source"`
	FetchedAt   time.Time         `json:"fetched_at"`
	Expressions map[string]string `json:"-"`
}

type priceRule struct {
	expr *expression
	refs map[string]bool
	err  error
}

func parsePrices(payload []byte, source string, at time.Time) (PriceSnapshot, map[string]priceRule, error) {
	if !ojson.ValidBytes(payload) {
		return PriceSnapshot{}, nil, fmt.Errorf("invalid pricing JSON")
	}
	root := ojson.NewDoc(payload)
	if success := root.Get("success"); success.Exists() && !success.Bool() {
		return PriceSnapshot{}, nil, fmt.Errorf("pricing source reported failure")
	}
	data := root.Get("data")
	expressions := data.Get("billing_expr")
	if !expressions.IsObject() {
		return PriceSnapshot{}, nil, fmt.Errorf("pricing source has no billing_expr object")
	}
	hash := sha256.Sum256(payload)
	snapshot := PriceSnapshot{Version: fmt.Sprintf("%x", hash), Source: source, FetchedAt: at, Expressions: make(map[string]string)}
	rules := make(map[string]priceRule)
	var invalid error
	expressions.ForEach(func(key, value ojson.Result) bool {
		model := strings.TrimSpace(key.String())
		if value.Type != ojson.String || model == "" {
			invalid = fmt.Errorf("invalid model billing expression")
			return false
		}
		if mode := data.Get("billing_mode." + ojson.Escape(model)); mode.Exists() && mode.String() != "tiered_expr" {
			invalid = fmt.Errorf("unsupported billing mode for %s", model)
			return false
		}
		expr, refs, errCompile := compileExpression(value.String())
		// Keep unsupported individual rules visible without discarding valid models.
		rules[model] = priceRule{expr: expr, refs: refs, err: errCompile}
		snapshot.Expressions[model] = value.String()
		return true
	})
	if invalid != nil {
		return PriceSnapshot{}, nil, invalid
	}
	if len(rules) == 0 || len(rules) > 20000 {
		return PriceSnapshot{}, nil, fmt.Errorf("pricing source has an invalid model count")
	}
	valid := false
	for _, rule := range rules {
		if rule.err == nil {
			valid = true
			break
		}
	}
	if !valid {
		return PriceSnapshot{}, nil, fmt.Errorf("pricing source contains no supported expressions")
	}
	return snapshot, rules, nil
}

type Valuation struct {
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	Model        string `json:"model,omitempty"`
	Tier         string `json:"tier,omitempty"`
	PriceVersion string `json:"price_version"`
	OutputUSD    string `json:"output_usd"`
	TotalUSD     string `json:"total_usd"`
}

func priceEvent(event Event, snapshot PriceSnapshot, rules map[string]priceRule) Valuation {
	result := Valuation{Status: "unpriced", PriceVersion: snapshot.Version, OutputUSD: "0", TotalUSD: "0"}
	if !event.UsageReported {
		result.Reason = "missing_usage"
		return result
	}
	if !event.Tokens.Valid() || event.Tokens.Quality != usage.TokenAccountingQualityComplete {
		result.Reason = "incomplete_token_breakdown"
		return result
	}
	model := event.PricingModel
	if model == "" {
		model = event.Model
	}
	result.Model = model
	rule, ok := rules[model]
	if !ok {
		result.Reason = "unknown_model"
		return result
	}
	if rule.err != nil {
		result.Reason = "unsupported_expression"
		return result
	}
	if event.AudioInput > 0 || event.AudioOutput > 0 || event.ImageOutput > 0 || event.ImageCount > 0 || rule.refs["image_count"] || rule.refs["ai"] || rule.refs["ao"] {
		result.Reason = "unsupported_media_pricing"
		return result
	}
	b := event.Tokens
	p := b.Input.TotalTokens
	if rule.refs["cr"] {
		p -= b.Input.CacheReadTokens
	}
	cc := b.Input.CacheWriteTokens
	if rule.refs["cc"] || rule.refs["cc1h"] || rule.refs["cc12h"] {
		p -= cc
	}
	if event.Cache1h < 0 || event.Cache12h < 0 || event.Cache1h > cc || event.Cache12h > cc-event.Cache1h {
		result.Reason = "inconsistent_cache_ttl"
		return result
	}
	if rule.refs["cc1h"] {
		cc -= event.Cache1h
	}
	if rule.refs["cc12h"] {
		cc -= event.Cache12h
	}
	if (rule.refs["cc1h"] || rule.refs["cc12h"]) && !rule.refs["cc"] && cc > 0 {
		result.Reason = "unsupported_cache_ttl"
		return result
	}
	tokens := map[string]int64{"p": p, "c": b.Output.TotalTokens, "cr": b.Input.CacheReadTokens, "cc": cc, "cc1h": event.Cache1h, "cc12h": event.Cache12h, "len": b.Input.TotalTokens}
	env := &priceEnvironment{tokens: tokens, params: event.PricingParams, at: event.RequestedAt}
	total, errTotal := rule.expr.eval(env)
	if errTotal != nil || total.kind != 'n' || total.n.Sign() < 0 {
		result.Reason = "invalid_pricing_context"
		return result
	}
	result.Tier = env.tier
	outputTokens := make(map[string]int64, len(tokens))
	for key, value := range tokens {
		if key == "len" || key == "c" || key == "ao" {
			outputTokens[key] = value
		}
	}
	output, errOutput := rule.expr.eval(&priceEnvironment{tokens: outputTokens, params: event.PricingParams, at: event.RequestedAt})
	inputTokens := make(map[string]int64, len(tokens))
	for key, value := range tokens {
		if key != "c" && key != "ao" {
			inputTokens[key] = value
		}
	}
	input, errInput := rule.expr.eval(&priceEnvironment{tokens: inputTokens, params: event.PricingParams, at: event.RequestedAt})
	baseline, errBaseline := rule.expr.eval(&priceEnvironment{tokens: map[string]int64{"len": tokens["len"]}, params: event.PricingParams, at: event.RequestedAt})
	if errOutput != nil || errInput != nil || errBaseline != nil || output.kind != 'n' || input.kind != 'n' || baseline.kind != 'n' {
		result.Reason = "invalid_output_price"
		return result
	}
	// Fixed fees are not output tokens. Cross-bucket expressions cannot be
	// decomposed reliably, so leave them unpriced instead of guessing.
	output.n.Sub(output.n, baseline.n)
	if output.n.Sign() < 0 || output.n.Cmp(total.n) > 0 || new(big.Rat).Add(input.n, output.n).Cmp(total.n) != 0 {
		result.Reason = "invalid_output_price"
		return result
	}
	result.OutputUSD = output.n.Quo(output.n, big.NewRat(1000000, 1)).FloatString(18)
	result.TotalUSD = total.n.Quo(total.n, big.NewRat(1000000, 1)).FloatString(18)
	result.Status = "priced"
	if event.Failed {
		result.Status = "partial"
		result.Reason = "failed_attempt_observed_usage"
	}
	return result
}

func addUSD(a, b string) string {
	x, okX := new(big.Rat).SetString(a)
	if !okX {
		x = new(big.Rat)
	}
	y, okY := new(big.Rat).SetString(b)
	if !okY {
		y = new(big.Rat)
	}
	return x.Add(x, y).FloatString(18)
}
