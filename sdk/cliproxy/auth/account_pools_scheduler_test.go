package auth

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type poolStatefulScheduler struct {
	fakePluginScheduler
	active bool
}

func (s *poolStatefulScheduler) HasScheduler() bool {
	return s.active
}

func TestAccountPoolsPluginHostAvailability(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, active := range []bool{false, true} {
			m, e := newPoolTestManager(t)
			scheduler := &poolStatefulScheduler{active: active}
			m.SetPluginScheduler(scheduler)
			var selected *Auth
			var err error
			if mixed {
				selected, _, _, err = m.pickNextMixed(poolTestContext("basic"), []string{e.Identifier()}, "pool-test-model", executor.Options{}, nil)
			} else {
				selected, _, err = m.pickNext(poolTestContext("basic"), e.Identifier(), "pool-test-model", executor.Options{}, nil)
			}
			if active {
				if err == nil || statusCodeFromError(err) != 501 {
					t.Fatalf("active scheduler accepted: mixed=%v err=%v", mixed, err)
				}
			} else if err != nil || selected == nil || selected.PoolID != "basic" {
				t.Fatalf("inactive host blocked native pool selection: mixed=%v err=%v", mixed, err)
			}
			if scheduler.calls != 0 {
				t.Fatal("scoped selection invoked plugin scheduler")
			}
		}
	}
}

func TestAccountPoolsInactiveSchedulerAndActivation(t *testing.T) {
	m, e := newPoolTestManager(t)
	inactive := &inactivePluginScheduler{}
	m.SetPluginScheduler(inactive)
	ctx := poolTestContext("basic")
	req := executor.Request{Model: "pool-test-model"}
	if _, err := m.SelectAuth(ctx, e.Identifier(), req.Model, executor.Options{}); err != nil {
		t.Fatalf("single-provider selection: %v", err)
	}
	if _, err := m.Execute(ctx, []string{e.Identifier()}, req, executor.Options{}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := m.ExecuteCount(ctx, []string{e.Identifier()}, req, executor.Options{}); err != nil {
		t.Fatalf("count: %v", err)
	}
	stream, err := m.ExecuteStream(ctx, []string{e.Identifier()}, req, executor.Options{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range stream.Chunks {
	}
	if inactive.calls != 0 || len(e.calls) != 3 {
		t.Fatalf("inactive scheduler calls=%d executor calls=%v", inactive.calls, e.calls)
	}
	for _, id := range e.calls {
		if id != "pool-a1" && id != "pool-a2" {
			t.Fatalf("foreign account used: %s", id)
		}
	}
	active := &fakePluginScheduler{}
	m.SetPluginScheduler(active)
	if _, err := m.SelectAuth(ctx, e.Identifier(), req.Model, executor.Options{}); err == nil || err.Error() != "pool_adapter_unsupported" {
		t.Fatalf("active scheduler single-provider gate: %v", err)
	}
	if _, err := m.Execute(ctx, []string{e.Identifier()}, req, executor.Options{}); err == nil || err.Error() != "pool_adapter_unsupported" {
		t.Fatalf("active scheduler mixed gate: %v", err)
	}
	if active.calls != 0 || len(e.calls) != 3 {
		t.Fatal("unsupported scheduler reached selection or execution")
	}
	m.SetPluginScheduler(inactive)
	if _, err := m.Execute(ctx, []string{e.Identifier()}, req, executor.Options{}); err != nil {
		t.Fatalf("deactivated scheduler did not restore routing: %v", err)
	}
}
