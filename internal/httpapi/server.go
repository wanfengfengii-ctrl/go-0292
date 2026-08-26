// Package httpapi exposes the versioned JSON HTTP API of the UHPC wet-joint
// traffic-release service: stable error codes, deterministic list ordering,
// transaction boundaries, and health/recovery status queries.
package httpapi

import (
	"encoding/json"
	"net/http"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/engine"
)

// Server is the HTTP handler for the service, wired to the application engine.
type Server struct {
	eng *engine.Engine
	mux *http.ServeMux
}

// New constructs a Server backed by an in-memory engine (single-process use).
func New() *Server {
	return NewWithEngine(engine.NewInMemory())
}

// NewWithEngine constructs a Server wired to an existing engine.
func NewWithEngine(eng *engine.Engine) *Server {
	s := &Server{eng: eng, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /v1/health", s.handleHealth)

	s.mux.HandleFunc("POST /v1/spans", s.handleRegisterSpan)
	s.mux.HandleFunc("POST /v1/recipes", s.handleRegisterRecipe)
	s.mux.HandleFunc("POST /v1/materials", s.handleStock)

	s.mux.HandleFunc("POST /v1/joints", s.handleCreateJoint)
	s.mux.HandleFunc("GET /v1/joints/{id}", s.handleGetJoint)
	s.mux.HandleFunc("GET /v1/joints/{id}/evidence", s.handleGetEvidence)
	s.mux.HandleFunc("POST /v1/joints/{id}/surface-evidence", s.handleSurfaceEvidence)
	s.mux.HandleFunc("POST /v1/joints/{id}/material-preparations", s.handleMaterialPreparation)
	s.mux.HandleFunc("POST /v1/joints/{id}/mix-runs", s.handleMixRun)
	s.mux.HandleFunc("POST /v1/joints/{id}/flow-tests", s.handleFlowTest)
	s.mux.HandleFunc("POST /v1/joints/{id}/fills", s.handleAppendFill)
	s.mux.HandleFunc("POST /v1/joints/{id}/curing-samples", s.handleCuring)
	s.mux.HandleFunc("POST /v1/joints/{id}/inspections", s.handleInspection)
	s.mux.HandleFunc("POST /v1/joints/{id}/retests", s.handleRetest)
	s.mux.HandleFunc("POST /v1/joints/{id}/reviews", s.handleReview)
	s.mux.HandleFunc("POST /v1/joints/{id}/verdicts", s.handleVerdict)

	s.mux.HandleFunc("POST /v1/device-calls/{key}/retry", s.handleDeviceRetry)
	s.mux.HandleFunc("POST /v1/device-calls/{key}/result", s.handleDeviceResult)

	s.mux.HandleFunc("POST /v1/retests/{id}/activate-generation", s.handleActivateGeneration)
	s.mux.HandleFunc("GET /v1/retests/{id}", s.handleGetRetest)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// handleHealth reports process and recovery status: 200 while writable, and 503
// RECOVERY_INTEGRITY_FAILED if recovery left the service read-only.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.eng.Healthy() {
		writeError(w, codes.New(codes.CodeRecoveryIntegrity, "service is read-only after recovery fault"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// statusForCode maps a stable domain code to a fixed HTTP status.
func statusForCode(c codes.Code) int {
	switch c {
	case codes.CodeIdempotencyConflict, codes.CodeStaleRuleDigest,
		codes.CodeMaterialExpired, codes.CodeLeaseConflict, codes.CodeLeaseExpired,
		codes.CodeLeaseNotHolder, codes.CodeAlreadyLocked, codes.CodeDuplicateReviewer,
		codes.CodeVerdictConflict, codes.CodeReviewConflict, codes.CodeDeviceDuplicate,
		codes.CodeDeviceOutOfOrder, codes.CodeDeviceRetryExceeded,
		codes.CodeZoneAlreadyRecorded, codes.CodePourDuplicate, codes.CodePourOutOfOrder,
		codes.CodeCuringOutOfOrder, codes.CodeDuplicateRetest,
		codes.CodePreconditionsNotMet, codes.CodeStaleGeneration:
		return http.StatusConflict
	case codes.CodeJointNotFound, codes.CodeRetestNotFound, codes.CodeOperationNotFound,
		codes.CodeDeviceCallNotFound:
		return http.StatusNotFound
	case codes.CodeNotQualified:
		return http.StatusForbidden
	case codes.CodeRecoveryIntegrity:
		return http.StatusServiceUnavailable
	default:
		return http.StatusUnprocessableEntity
	}
}

// writeError writes a stable domain error as JSON with a fixed HTTP status.
func writeError(w http.ResponseWriter, err error) {
	code := codes.CodeOf(err)
	status := statusForCode(code)
	if status == 0 {
		status = http.StatusUnprocessableEntity
	}
	msg := "unknown error"
	if err != nil {
		msg = err.Error()
	}
	writeJSON(w, status, map[string]string{
		"code":    string(code),
		"message": msg,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON decodes a request body into v, rejecting malformed bodies.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, codes.New(codes.CodeDeviceFormat, "invalid JSON body: "+err.Error()))
		return false
	}
	return true
}

// pathID extracts a path wildcard value.
func pathID(r *http.Request, name string) string { return r.PathValue(name) }
