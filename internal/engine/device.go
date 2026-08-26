package engine

import (
	"fmt"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

// MaxDeviceRetries is the fixed maximum number of retries for an external
// instrument call before it is permanently exhausted.
const MaxDeviceRetries = 3

// RecordDeviceFailure registers a bounded, deterministic retry record for an
// external instrument refusal, disconnect, timeout or format error.
func (e *Engine) RecordDeviceFailure(call domain.DeviceCall) error {
	return e.mutate("device-failure", func(st *state) error {
		existing, ok := st.DeviceCalls[call.Key]
		if ok && existing.Status == domain.CallSuccess {
			return codes.New(codes.CodeDeviceDuplicate, "success already consumed for "+call.Key)
		}
		expected := int64(1)
		if ok {
			expected = int64(existing.Attempt) + 1
		}
		if int64(call.Attempt) != expected {
			return codes.New(codes.CodeDeviceOutOfOrder,
				fmt.Sprintf("expected attempt %d, got %d", expected, call.Attempt))
		}
		if call.Attempt > MaxDeviceRetries {
			return codes.New(codes.CodeDeviceRetryExceeded, "device retry limit exceeded")
		}
		st.DeviceCalls[call.Key] = domain.DeviceCall{
			Key:        call.Key,
			Instrument: call.Instrument,
			Attempt:    call.Attempt,
			Failure:    call.Failure,
			Time:       call.Time,
			Status:     domain.CallPending,
		}
		st.advanceNow(call.Time)
		st.nextSequence()
		return nil
	})
}

// RecordDeviceResult consumes a successful device result exactly once. A
// malformed (negative) reading is rejected as a format error.
func (e *Engine) RecordDeviceResult(call domain.DeviceCall) error {
	return e.mutate("device-result", func(st *state) error {
		existing, ok := st.DeviceCalls[call.Key]
		if ok && existing.Status == domain.CallSuccess {
			return codes.New(codes.CodeDeviceDuplicate, "success already consumed for "+call.Key)
		}
		expected := int64(1)
		if ok {
			expected = int64(existing.Attempt) + 1
		}
		if int64(call.Attempt) != expected {
			return codes.New(codes.CodeDeviceOutOfOrder,
				fmt.Sprintf("expected attempt %d, got %d", expected, call.Attempt))
		}
		if call.Reading.Sign() < 0 {
			return codes.New(codes.CodeDeviceFormat, "reading has invalid format")
		}
		st.DeviceCalls[call.Key] = domain.DeviceCall{
			Key:        call.Key,
			Instrument: call.Instrument,
			Attempt:    call.Attempt,
			Reading:    call.Reading,
			Time:       call.Time,
			Status:     domain.CallSuccess,
		}
		st.advanceNow(call.Time)
		st.nextSequence()
		return nil
	})
}
