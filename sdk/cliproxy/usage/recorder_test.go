package usage

import (
	"context"
	"fmt"
	"testing"
)

type captureRecorder struct {
	records  []Record
	canceled bool
	fail     bool
}

func (r *captureRecorder) Record(ctx context.Context, record Record) error {
	r.records = append(r.records, record)
	r.canceled = ctx.Err() != nil
	if r.fail {
		return fmt.Errorf("test commit failure")
	}
	return nil
}

func TestRecorderCommitsSynchronouslyWithCapturedIdentity(t *testing.T) {
	m := NewManager(1)
	defer m.Stop()
	r := &captureRecorder{}
	ctx, cancel := context.WithCancel(WithRecorder(WithAPIKey(context.Background(), "client-key"), r))
	cancel()
	m.Publish(ctx, Record{Model: "model", AuthIndex: "account"})
	if len(r.records) != 1 || r.records[0].RequestID == "" || r.records[0].APIKey != "client-key" || r.canceled {
		t.Fatalf("recorder not committed synchronously: %+v", r)
	}
	m.Publish(context.Background(), Record{})
	if len(r.records) != 1 {
		t.Fatal("recorder leaked into another request")
	}
}

func TestRecorderFailureDoesNotAbortUsagePublishing(t *testing.T) {
	m := NewManager(1)
	defer m.Stop()
	r := &captureRecorder{fail: true}
	m.Publish(WithRecorder(context.Background(), r), Record{RequestID: "execution"})
	if len(r.records) != 1 {
		t.Fatal("missing recording attempt")
	}
}

func TestRecorderInheritsRequestScope(t *testing.T) {
	r := &captureRecorder{}
	request := WithRecorder(context.Background(), r)
	ctx := InheritRecorder(context.Background(), request)
	if recorderFromContext(ctx) != r {
		t.Fatal("request recorder was lost")
	}
}
