package engine

import (
	"sort"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

// JointView is the read-only aggregate snapshot of a joint returned to the API
// layer.
type JointView struct {
	JointNumber  string                      `json:"joint_number"`
	SpanID       string                      `json:"span_id"`
	Recipe       string                      `json:"recipe"`
	Summary      domain.LockSummary          `json:"summary"`
	Prefix       domain.PourPrefix           `json:"prefix"`
	Generation   string                      `json:"generation"`
	SurfaceZones []domain.SurfaceZone        `json:"surface_zones"`
	Surface      []domain.SurfaceEvidence    `json:"surface"`
	Fills        []domain.FillCell           `json:"fills"`
	Curing       []domain.CuringEvidence     `json:"curing"`
	CuringClosed bool                        `json:"curing_closed"`
	FlowPassed   bool                        `json:"flow_passed"`
	Inspections  []domain.InspectionEvidence `json:"inspections"`
	RetestID     string                      `json:"retest_id"`
	Verdict      *domain.FinalVerdict        `json:"verdict"`
	Reviews      int                         `json:"reviews"`
}

// GetJoint returns a read-only view of a joint's current aggregate state.
func (e *Engine) GetJoint(jointID string) (JointView, error) {
	var view JointView
	err := e.read(func(st *state) error {
		j, ok := st.Joints[jointID]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}
		view = jointViewOf(j)
		if v, ok := st.Verdicts[jointID]; ok {
			cp := v
			view.Verdict = &cp
		}
		view.Reviews = len(st.Reviews[jointID])
		return nil
	})
	return view, err
}

func jointViewOf(j *jointState) JointView {
	view := JointView{
		JointNumber:  j.Design.JointNumber,
		SpanID:       j.SpanID,
		Recipe:       j.Design.Recipe,
		Summary:      j.Summary,
		Prefix:       prefixFromFills(j),
		Generation:   j.Generation,
		SurfaceZones: append([]domain.SurfaceZone(nil), j.Design.SurfaceZones...),
		Curing:       append([]domain.CuringEvidence(nil), j.Curing...),
		CuringClosed: j.CuringClosed,
		Fills:        append([]domain.FillCell(nil), j.Fills...),
		Inspections:  append([]domain.InspectionEvidence(nil), j.Inspections...),
		RetestID:     j.RetestID,
	}
	if j.Flow != nil {
		view.FlowPassed = j.Flow.Passed
	}
	for _, z := range j.Design.SurfaceZones {
		if ev, ok := j.Surface[z.ID]; ok {
			view.Surface = append(view.Surface, ev)
		}
	}
	sort.Slice(view.Surface, func(i, k int) bool { return view.Surface[i].ZoneID < view.Surface[k].ZoneID })
	return view
}

// GetRetest returns the retest set and any remediation generation for it.
func (e *Engine) GetRetest(retestID string) (domain.RetestSet, domain.RemediationGeneration, bool, error) {
	var rs domain.RetestSet
	var rem domain.RemediationGeneration
	var hasRem bool
	err := e.read(func(st *state) error {
		var ok bool
		rs, ok = st.Retests[retestID]
		if !ok {
			return codes.New(codes.CodeRetestNotFound, "retest "+retestID+" not found")
		}
		rem, hasRem = st.Remediations[retestID]
		return nil
	})
	return rs, rem, hasRem, err
}

// GetDeviceCall returns the current device call record for a key.
func (e *Engine) GetDeviceCall(key string) (domain.DeviceCall, bool, error) {
	var call domain.DeviceCall
	var ok bool
	err := e.read(func(st *state) error {
		call, ok = st.DeviceCalls[key]
		return nil
	})
	return call, ok, err
}
