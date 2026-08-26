package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

// handleCreateJoint validates and locks a joint design.
func (s *Server) handleCreateJoint(w http.ResponseWriter, r *http.Request) {
	var design domain.JointDesign
	if !decodeJSON(w, r, &design) {
		return
	}
	summary, err := s.eng.Lock(design.JointNumber, design)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, summary)
}

// handleGetJoint returns the current aggregate view of a joint.
func (s *Server) handleGetJoint(w http.ResponseWriter, r *http.Request) {
	view, err := s.eng.GetJoint(pathID(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleGetEvidence returns a joint's material/evidence snapshot.
func (s *Server) handleGetEvidence(w http.ResponseWriter, r *http.Request) {
	view, err := s.eng.GetJoint(pathID(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"prefix":        view.Prefix,
		"generation":    view.Generation,
		"surface":       view.Surface,
		"fills":         view.Fills,
		"curing":        view.Curing,
		"curing_closed": view.CuringClosed,
		"inspections":   view.Inspections,
		"retest_id":     view.RetestID,
		"verdict":       view.Verdict,
	})
}

// handleSurfaceEvidence appends base-surface evidence.
func (s *Server) handleSurfaceEvidence(w http.ResponseWriter, r *http.Request) {
	var ev domain.SurfaceEvidence
	if !decodeJSON(w, r, &ev) {
		return
	}
	if err := s.eng.RecordSurfaceEvidence(pathID(r, "id"), ev); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ev)
}

// prepareRequest carries an operation ID plus the atomic material/lease request.
type prepareRequest struct {
	OperationID string `json:"operation_id"`
	domain.MaterialRequest
}

// handleMaterialPreparation atomically deducts materials and acquires leases.
func (s *Server) handleMaterialPreparation(w http.ResponseWriter, r *http.Request) {
	var req prepareRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.OperationID == "" {
		writeError(w, codes.New(codes.CodeOperationNotFound, "operation_id is required"))
		return
	}
	op := domain.OperationRecord{
		OperationID: req.OperationID,
		Digest:      materialDigest(req.MaterialRequest),
	}
	leases, err := s.eng.Prepare(op, req.MaterialRequest)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, leases)
}

// handleMixRun records a completed mix run.
func (s *Server) handleMixRun(w http.ResponseWriter, r *http.Request) {
	var run domain.MixRun
	if !decodeJSON(w, r, &run) {
		return
	}
	gen, err := s.eng.RecordMix(pathID(r, "id"), run)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, gen)
}

// handleFlowTest records a flow test result.
func (s *Server) handleFlowTest(w http.ResponseWriter, r *http.Request) {
	var flow domain.FlowTest
	if !decodeJSON(w, r, &flow) {
		return
	}
	if err := s.eng.RecordFlow(pathID(r, "id"), flow); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, flow)
}

// handleAppendFill appends a fill cell and returns the new prefix.
func (s *Server) handleAppendFill(w http.ResponseWriter, r *http.Request) {
	var cell domain.FillCell
	if !decodeJSON(w, r, &cell) {
		return
	}
	prefix, err := s.eng.AppendFill(pathID(r, "id"), cell)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, prefix)
}

// handleCuring appends a curing sample.
func (s *Server) handleCuring(w http.ResponseWriter, r *http.Request) {
	var ev domain.CuringEvidence
	if !decodeJSON(w, r, &ev) {
		return
	}
	if err := s.eng.RecordCuring(pathID(r, "id"), ev); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ev)
}

// handleInspection appends inspection evidence.
func (s *Server) handleInspection(w http.ResponseWriter, r *http.Request) {
	var ev domain.InspectionEvidence
	if !decodeJSON(w, r, &ev) {
		return
	}
	if err := s.eng.RecordInspection(pathID(r, "id"), ev); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ev)
}

// handleRetest computes the unique retest closure for an anomaly.
func (s *Server) handleRetest(w http.ResponseWriter, r *http.Request) {
	var anomaly domain.Anomaly
	if !decodeJSON(w, r, &anomaly) {
		return
	}
	if anomaly.JointNumber == "" {
		anomaly.JointNumber = pathID(r, "id")
	}
	set, err := s.eng.Retest(anomaly)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, set)
}

// handleReview records an independent reviewer conclusion.
func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	var review domain.Review
	if !decodeJSON(w, r, &review) {
		return
	}
	if err := s.eng.SubmitReview(pathID(r, "id"), review); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, review)
}

// handleVerdict executes the single-writer final verdict barrier.
func (s *Server) handleVerdict(w http.ResponseWriter, r *http.Request) {
	var verdict domain.FinalVerdict
	if !decodeJSON(w, r, &verdict) {
		return
	}
	result, err := s.eng.Verdict(pathID(r, "id"), verdict)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// materialDigest computes a stable content digest for idempotency.
func materialDigest(req domain.MaterialRequest) string {
	raw, _ := json.Marshal(req)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
