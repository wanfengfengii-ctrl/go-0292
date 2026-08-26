package engine

import (
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

// TestFlowWindowFixedPoint verifies the fixed-point flow window (18.0..26.0 at
// scale 1) accepts in-window values and rejects out-of-window values.
func TestFlowWindowFixedPoint(t *testing.T) {
	e := NewInMemory()
	registerSpanRecipe(t, e)
	if _, err := e.Lock("J1", standardDesign()); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := e.RecordFlow("J1", domain.FlowTest{Value: fpT(200, 1), Passed: true, Time: 10}); err != nil {
		t.Fatalf("in-window flow rejected: %v", err)
	}
	if err := e.RecordFlow("J1", domain.FlowTest{Value: fpT(170, 1), Passed: true, Time: 11}); codes.CodeOf(err) != codes.CodeFlowFailed {
		t.Fatalf("below-window flow: got %v, want FLOW_FAILED", err)
	}
	if err := e.RecordFlow("J1", domain.FlowTest{Value: fpT(265, 1), Passed: false, Time: 12}); codes.CodeOf(err) != codes.CodeFlowFailed {
		t.Fatalf("failed flow: got %v, want FLOW_FAILED", err)
	}
}

// TestDeviceRetryBounded verifies scripted instrument failures produce bounded,
// deterministic pending retry records.
func TestDeviceRetryBounded(t *testing.T) {
	e := NewInMemory()
	failures := []struct {
		attempt int
		failure string
	}{
		{1, "SCALE_REFUSED"},
		{2, "FLOW_TIMEOUT"},
		{3, "PRESS_FORMAT"},
	}
	for _, f := range failures {
		if err := e.RecordDeviceFailure(domain.DeviceCall{
			Key: "call-1", Instrument: "SCALE", Attempt: f.attempt, Failure: f.failure, Time: domain.LogicalTime(f.attempt),
		}); err != nil {
			t.Fatalf("failure attempt %d: %v", f.attempt, err)
		}
	}
	// Exceeding the fixed retry limit is rejected.
	err := e.RecordDeviceFailure(domain.DeviceCall{Key: "call-1", Instrument: "SCALE", Attempt: 4, Failure: "X", Time: 4})
	if codes.CodeOf(err) != codes.CodeDeviceRetryExceeded {
		t.Fatalf("attempt 4: got %v, want DEVICE_RETRY_EXCEEDED", err)
	}
	call, ok, _ := e.GetDeviceCall("call-1")
	if !ok || call.Status != domain.CallPending || call.Attempt != 3 {
		t.Fatalf("call-1 = %+v, want pending attempt 3", call)
	}
}

// TestDeviceOutOfOrder verifies attempts must be strictly sequential.
func TestDeviceOutOfOrder(t *testing.T) {
	e := NewInMemory()
	if err := e.RecordDeviceFailure(domain.DeviceCall{Key: "k", Instrument: "SCALE", Attempt: 1, Failure: "REFUSED", Time: 1}); err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	err := e.RecordDeviceFailure(domain.DeviceCall{Key: "k", Instrument: "SCALE", Attempt: 3, Failure: "REFUSED", Time: 3})
	if codes.CodeOf(err) != codes.CodeDeviceOutOfOrder {
		t.Fatalf("attempt 3: got %v, want DEVICE_OUT_OF_ORDER", err)
	}
}

// TestDeviceResultFormatAndDuplicate verifies a malformed reading is rejected as
// a format error and a successful result is consumed exactly once.
func TestDeviceResultFormatAndDuplicate(t *testing.T) {
	e := NewInMemory()
	// Negative reading is a format error.
	err := e.RecordDeviceResult(domain.DeviceCall{Key: "p", Instrument: "PRESS", Attempt: 1, Reading: fpT(-1, 0), Time: 1})
	if codes.CodeOf(err) != codes.CodeDeviceFormat {
		t.Fatalf("format error: got %v, want DEVICE_FORMAT_ERROR", err)
	}
	// Valid result succeeds.
	if err := e.RecordDeviceResult(domain.DeviceCall{Key: "p", Instrument: "PRESS", Attempt: 1, Reading: fpT(50, 0), Time: 1}); err != nil {
		t.Fatalf("valid result: %v", err)
	}
	// Success consumed once: a second result is rejected.
	err = e.RecordDeviceResult(domain.DeviceCall{Key: "p", Instrument: "PRESS", Attempt: 2, Reading: fpT(51, 0), Time: 2})
	if codes.CodeOf(err) != codes.CodeDeviceDuplicate {
		t.Fatalf("duplicate result: got %v, want DEVICE_DUPLICATE", err)
	}
}
