package httpapi

import (
	"net/http"

	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

// handleRegisterSpan registers a bridge span.
func (s *Server) handleRegisterSpan(w http.ResponseWriter, r *http.Request) {
	var span domain.BridgeSpan
	if !decodeJSON(w, r, &span) {
		return
	}
	if err := s.eng.RegisterSpan(span); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, span)
}

// handleRegisterRecipe registers a UHPC recipe revision.
func (s *Server) handleRegisterRecipe(w http.ResponseWriter, r *http.Request) {
	var rule domain.RecipeRule
	if !decodeJSON(w, r, &rule) {
		return
	}
	if err := s.eng.RegisterRecipe(rule); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

// handleStock credits grams into a material pool.
func (s *Server) handleStock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Category domain.MaterialCategory `json:"category"`
		Batch    string                  `json:"batch"`
		Grams    int64                   `json:"grams"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.eng.Stock(req.Category, req.Batch, req.Grams); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

// handleDeviceRetry registers a failed instrument attempt.
func (s *Server) handleDeviceRetry(w http.ResponseWriter, r *http.Request) {
	var call domain.DeviceCall
	if !decodeJSON(w, r, &call) {
		return
	}
	call.Key = pathID(r, "key")
	if err := s.eng.RecordDeviceFailure(call); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, call)
}

// handleDeviceResult consumes a successful instrument result.
func (s *Server) handleDeviceResult(w http.ResponseWriter, r *http.Request) {
	var call domain.DeviceCall
	if !decodeJSON(w, r, &call) {
		return
	}
	call.Key = pathID(r, "key")
	if err := s.eng.RecordDeviceResult(call); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, call)
}

// handleActivateGeneration activates a remediation generation.
func (s *Server) handleActivateGeneration(w http.ResponseWriter, r *http.Request) {
	rem, err := s.eng.ActivateGeneration(pathID(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rem)
}

// handleGetRetest returns a retest set and its remediation lineage.
func (s *Server) handleGetRetest(w http.ResponseWriter, r *http.Request) {
	rs, rem, hasRem, err := s.eng.GetRetest(pathID(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"retest":      rs,
		"remediation": mapOrNil(rem, hasRem),
	})
}

func mapOrNil(rem domain.RemediationGeneration, ok bool) any {
	if !ok {
		return nil
	}
	return rem
}
