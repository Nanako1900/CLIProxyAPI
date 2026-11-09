// Package accounting persists credential-attributed usage and API-list-price estimates.
package accounting

import (
	"fmt"
	"math"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

type Event struct {
	ID                string               `json:"execution_id"`
	ParentExecutionID string               `json:"parent_execution_id,omitempty"`
	TraceID           string               `json:"trace_id,omitempty"`
	AccountID         string               `json:"account_uid"`
	AuthID            string               `json:"auth_id,omitempty"`
	AuthIndex         string               `json:"auth_index,omitempty"`
	Provider          string               `json:"provider"`
	Model             string               `json:"model"`
	Alias             string               `json:"alias,omitempty"`
	UpstreamModel     string               `json:"upstream_model,omitempty"`
	ResponseModel     string               `json:"response_model,omitempty"`
	PricingModel      string               `json:"pricing_model"`
	PricingParams     map[string]bool      `json:"-"`
	RequestedAt       time.Time            `json:"requested_at"`
	RecordedAt        time.Time            `json:"recorded_at"`
	Failed            bool                 `json:"failed"`
	Stream            bool                 `json:"stream"`
	UsageReported     bool                 `json:"usage_reported"`
	ServiceTier       string               `json:"service_tier,omitempty"`
	Tokens            usage.TokenBreakdown `json:"token_breakdown"`
	Cache1h           int64                `json:"cache_creation_1h_tokens,omitempty"`
	Cache12h          int64                `json:"cache_creation_12h_tokens,omitempty"`
	AudioInput        int64                `json:"audio_input_tokens,omitempty"`
	AudioOutput       int64                `json:"audio_output_tokens,omitempty"`
	ImageOutput       int64                `json:"image_output_tokens,omitempty"`
	ImageCount        int64                `json:"image_count,omitempty"`
	Valuation         Valuation            `json:"valuation"`
}

type Totals struct {
	Executions         int64  `json:"executions"`
	Success            int64  `json:"success"`
	Failed             int64  `json:"failed"`
	UsageEvents        int64  `json:"usage_events"`
	InputTokens        int64  `json:"input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	ReasoningTokens    int64  `json:"reasoning_tokens"`
	CacheReadTokens    int64  `json:"cache_read_tokens"`
	CacheWriteTokens   int64  `json:"cache_write_tokens"`
	UnclassifiedTokens int64  `json:"unclassified_tokens"`
	Priced             int64  `json:"priced_events"`
	Partial            int64  `json:"partial_events"`
	Unpriced           int64  `json:"unpriced_events"`
	MissingUsage       int64  `json:"missing_usage_events"`
	OutputUSD          string `json:"output_usd"`
	TotalUSD           string `json:"total_usd"`
}

func (t *Totals) addEvent(e Event) error {
	other := Totals{UsageEvents: 1, InputTokens: e.Tokens.Input.TotalTokens, OutputTokens: e.Tokens.Output.TotalTokens, ReasoningTokens: e.Tokens.Output.ReasoningTokens, CacheReadTokens: e.Tokens.Input.CacheReadTokens, CacheWriteTokens: e.Tokens.Input.CacheWriteTokens, UnclassifiedTokens: e.Tokens.UnclassifiedTokens, OutputUSD: e.Valuation.OutputUSD, TotalUSD: e.Valuation.TotalUSD}
	if e.ParentExecutionID == "" {
		other.Executions = 1
		if e.Failed {
			other.Failed = 1
		} else {
			other.Success = 1
		}
	}
	if !e.UsageReported {
		other.MissingUsage = 1
	}
	switch e.Valuation.Status {
	case "priced":
		other.Priced = 1
	case "partial":
		other.Partial = 1
	default:
		other.Unpriced = 1
	}
	return t.merge(other)
}

func (t *Totals) merge(other Totals) error {
	merged := *t
	for _, pair := range []struct {
		dst *int64
		src int64
	}{
		{&merged.Executions, other.Executions}, {&merged.Success, other.Success}, {&merged.Failed, other.Failed},
		{&merged.UsageEvents, other.UsageEvents}, {&merged.InputTokens, other.InputTokens}, {&merged.OutputTokens, other.OutputTokens},
		{&merged.ReasoningTokens, other.ReasoningTokens}, {&merged.CacheReadTokens, other.CacheReadTokens}, {&merged.CacheWriteTokens, other.CacheWriteTokens},
		{&merged.UnclassifiedTokens, other.UnclassifiedTokens}, {&merged.Priced, other.Priced}, {&merged.Partial, other.Partial},
		{&merged.Unpriced, other.Unpriced}, {&merged.MissingUsage, other.MissingUsage},
	} {
		if pair.src < 0 || *pair.dst < 0 || *pair.dst > math.MaxInt64-pair.src {
			return fmt.Errorf("usage totals exceed the non-negative int64 range")
		}
		*pair.dst += pair.src
	}
	merged.OutputUSD = addUSD(t.OutputUSD, other.OutputUSD)
	merged.TotalUSD = addUSD(t.TotalUSD, other.TotalUSD)
	*t = merged
	return nil
}

type Account struct {
	ID        string    `json:"account_uid"`
	Provider  string    `json:"provider"`
	Label     string    `json:"label"`
	AuthIndex string    `json:"auth_index,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Totals    Totals    `json:"totals"`
}

type DailyTotal struct {
	AccountID string `json:"account_uid"`
	Day       string `json:"day"`
	Model     string `json:"model"`
	Totals    Totals `json:"totals"`
}

type Status struct {
	Enabled        bool      `json:"enabled"`
	Available      bool      `json:"available"`
	StartedAt      time.Time `json:"started_at"`
	Pending        int       `json:"pending_events"`
	LastError      string    `json:"last_error,omitempty"`
	PriceError     string    `json:"price_error,omitempty"`
	PriceVersion   string    `json:"price_version,omitempty"`
	PriceFetchedAt time.Time `json:"price_fetched_at,omitempty"`
	PriceSource    string    `json:"price_source,omitempty"`
	Coverage       string    `json:"coverage"`
}

type Query struct {
	From     string
	To       string
	Model    string
	Provider string
	Sort     string
	Offset   int
	Limit    int
}
