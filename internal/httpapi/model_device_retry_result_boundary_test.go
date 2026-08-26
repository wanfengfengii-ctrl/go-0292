package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/engine"
	"example.com/uhpc-wet-joint-traffic-release/internal/fixedpoint"
)

func TestModel_DeviceResultSharesRetryBoundary(t *testing.T) {
	type step struct {
		endpoint string
		attempt  int
		reading  int64
		failure  string
	}
	tests := []struct {
		name        string
		instrument  string
		setup       []step
		final       step
		wantHTTP    int
		wantCode    codes.Code
		wantExists  bool
		wantAttempt int
		wantStatus  domain.CallStatus
	}{
		{
			name: "first valid result succeeds", instrument: "PRESS",
			final:    step{endpoint: "result", attempt: 1, reading: 50},
			wantHTTP: http.StatusCreated, wantExists: true, wantAttempt: 1, wantStatus: domain.CallSuccess,
		},
		{
			name: "sequential retries remain valid", instrument: "FLOW",
			setup:    []step{{endpoint: "retry", attempt: 1, failure: "FLOW_TIMEOUT"}},
			final:    step{endpoint: "retry", attempt: 2, failure: "FLOW_TIMEOUT"},
			wantHTTP: http.StatusCreated, wantExists: true, wantAttempt: 2, wantStatus: domain.CallPending,
		},
		{
			name: "out of order result is rejected", instrument: "PRESS",
			setup:    []step{{endpoint: "retry", attempt: 1, failure: "PRESS_TIMEOUT"}},
			final:    step{endpoint: "result", attempt: 3, reading: 50},
			wantHTTP: http.StatusConflict, wantCode: codes.CodeDeviceOutOfOrder,
			wantExists: true, wantAttempt: 1, wantStatus: domain.CallPending,
		},
		{
			name: "negative result is rejected", instrument: "PRESS",
			final:    step{endpoint: "result", attempt: 1, reading: -1},
			wantHTTP: http.StatusUnprocessableEntity, wantCode: codes.CodeDeviceFormat,
		},
		{
			name: "result after success is rejected", instrument: "FLOW",
			setup:    []step{{endpoint: "result", attempt: 1, reading: 20}},
			final:    step{endpoint: "result", attempt: 2, reading: 21},
			wantHTTP: http.StatusConflict, wantCode: codes.CodeDeviceDuplicate,
			wantExists: true, wantAttempt: 1, wantStatus: domain.CallSuccess,
		},
		{
			name: "pressure result after three failures exceeds retry limit", instrument: "PRESS",
			setup: []step{
				{endpoint: "retry", attempt: 1, failure: "PRESS_TIMEOUT"},
				{endpoint: "retry", attempt: 2, failure: "PRESS_TIMEOUT"},
				{endpoint: "retry", attempt: 3, failure: "PRESS_TIMEOUT"},
			},
			final:    step{endpoint: "result", attempt: 4, reading: 50},
			wantHTTP: http.StatusConflict, wantCode: codes.CodeDeviceRetryExceeded,
			wantExists: true, wantAttempt: 3, wantStatus: domain.CallPending,
		},
		{
			name: "flow result after three failures exceeds retry limit", instrument: "FLOW",
			setup: []step{
				{endpoint: "retry", attempt: 1, failure: "FLOW_TIMEOUT"},
				{endpoint: "retry", attempt: 2, failure: "FLOW_TIMEOUT"},
				{endpoint: "retry", attempt: 3, failure: "FLOW_TIMEOUT"},
			},
			final:    step{endpoint: "result", attempt: 4, reading: 20},
			wantHTTP: http.StatusConflict, wantCode: codes.CodeDeviceRetryExceeded,
			wantExists: true, wantAttempt: 3, wantStatus: domain.CallPending,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			eng := engine.NewInMemory()
			srv := NewWithEngine(eng)
			key := "shared-retry-key"
			send := func(s step) *httptest.ResponseRecorder {
				reading, err := fixedpoint.New(s.reading, 0)
				if err != nil {
					t.Fatalf("fixedpoint.New: %v", err)
				}
				body, err := json.Marshal(domain.DeviceCall{
					Instrument: tc.instrument,
					Attempt:    s.attempt,
					Failure:    s.failure,
					Reading:    reading,
					Time:       domain.LogicalTime(s.attempt),
				})
				if err != nil {
					t.Fatalf("json.Marshal: %v", err)
				}
				req := httptest.NewRequest(http.MethodPost, "/v1/device-calls/"+key+"/"+s.endpoint, bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rr := httptest.NewRecorder()
				srv.ServeHTTP(rr, req)
				return rr
			}

			for i, s := range tc.setup {
				if rr := send(s); rr.Code != http.StatusCreated {
					t.Fatalf("setup step %d: got HTTP %d body %s, want %d", i+1, rr.Code, rr.Body.String(), http.StatusCreated)
				}
			}
			rr := send(tc.final)
			if rr.Code != tc.wantHTTP {
				t.Errorf("final response: got HTTP %d body %s, want %d", rr.Code, rr.Body.String(), tc.wantHTTP)
			}
			if tc.wantCode != "" {
				var response struct {
					Code codes.Code `json:"code"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode error response: %v", err)
				}
				if response.Code != tc.wantCode {
					t.Errorf("final response code: got %q, want %q", response.Code, tc.wantCode)
				}
			}

			call, exists, err := eng.GetDeviceCall(key)
			if err != nil {
				t.Fatalf("GetDeviceCall: %v", err)
			}
			if exists != tc.wantExists {
				t.Fatalf("DeviceCalls[%q] existence: got %t, want %t", key, exists, tc.wantExists)
			}
			if exists && (call.Attempt != tc.wantAttempt || call.Status != tc.wantStatus) {
				t.Errorf("DeviceCalls[%q]: got attempt %d status %s, want attempt %d status %s", key, call.Attempt, call.Status, tc.wantAttempt, tc.wantStatus)
			}
		})
	}
}
