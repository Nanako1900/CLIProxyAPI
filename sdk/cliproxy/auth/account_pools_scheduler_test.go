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
