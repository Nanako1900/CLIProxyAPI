package auth

import (
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestAccountPoolCooldownUsesScopedStrategy(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Hour)
	for _, tc := range []struct {
		name          string
		global        Selector
		poolStrategy  string
		wantAvailable bool
	}{
		{"weighted pool excludes zero weight", &RoundRobinSelector{}, "weighted-round-robin", false},
		{"round robin pool includes zero weight", &WeightedRoundRobinSelector{}, "round-robin", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, e := newPoolTestManager(t)
			cfg := poolTestConfig()
			cfg.AccountPools.Pools[0].Strategy = tc.poolStrategy
			m.SetConfig(cfg)
			m.SetSelector(tc.global)
			m.mu.Lock()
			m.auths["pool-a1"].ModelStates = map[string]*ModelState{
				"pool-test-model": {
					Unavailable:    true,
					NextRetryAfter: deadline,
					Quota:          QuotaState{Exceeded: true, NextRecoverAt: deadline},
				},
			}
			m.auths["pool-a2"].Attributes = map[string]string{"weight": "0"}
			m.mu.Unlock()
			// Foreign and unassigned credentials stay available; neither can
			// suppress the selected pool's cooldown or change its reset time.
			ctx := poolTestContext("basic")
			earliest, available, cooling := m.earliestPoolCooldownForModelAt(ctx, []string{e.Identifier()}, "pool-test-model", cliproxyexecutor.Options{}, now)
			if available != tc.wantAvailable || cooling == tc.wantAvailable {
				t.Fatalf("available=%v cooling=%v, want available=%v", available, cooling, tc.wantAvailable)
			}
			if cooling && !earliest.Equal(deadline) {
				t.Fatalf("deadline=%v, want %v", earliest, deadline)
			}
			baseErr := newIssue7521Upstream429("selected pool exhausted", 3*time.Hour)
			got := m.annotatePoolCooldownTerminalErrorAt(ctx, []string{e.Identifier()}, "pool-test-model", cliproxyexecutor.Options{}, baseErr, time.Time{}, now)
			cooldown, annotated := got.(*poolCooldownTerminalError)
			if annotated == tc.wantAvailable {
				t.Fatalf("annotated=%v, want %v", annotated, !tc.wantAvailable)
			}
			if annotated && !cooldown.deadline.Equal(deadline) {
				t.Fatalf("annotated deadline=%v, want %v", cooldown.deadline, deadline)
			}
		})
	}
}
