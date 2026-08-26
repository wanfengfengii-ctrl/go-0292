package engine

import (
	"fmt"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/fixedpoint"
)

// RecordMix records a completed mix run, producing a non-mergeable material
// generation with a deterministic work deadline. It requires a confirmed
// surface, a matching plan, and doses within the recipe's allowable deviation
// (fiber must be exactly conserved).
func (e *Engine) RecordMix(jointID string, run domain.MixRun) (domain.MaterialGeneration, error) {
	var gen domain.MaterialGeneration
	err := e.mutate("record-mix", func(st *state) error {
		j, ok := st.Joints[jointID]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}

		rule, ok := st.Recipes[j.Design.Recipe]
		if !ok {
			return codes.New(codes.CodeSpanRecipeMismatch, "recipe "+j.Design.Recipe+" not registered")
		}

		if confirmed, err := surfaceConfirmed(j); err != nil {
			return err
		} else if !confirmed {
			return codes.New(codes.CodeSurfaceNotConfirmed, "surface handover is not confirmed")
		}

		// Mixing must proceed in locked plan order.
		if run.Sequence != j.MixCount {
			return codes.New(codes.CodePourOutOfOrder,
				fmt.Sprintf("expected mix sequence %d, got %d", j.MixCount, run.Sequence))
		}
		plan, err := planFor(j.Design.MixPlans, run.Sequence)
		if err != nil {
			return err
		}
		if plan.Batch != run.Batch {
			return codes.New(codes.CodePourWrongBatch, "batch does not match plan")
		}

		// Dosage deviation for powder/water/admixture; exact fiber conservation.
		for _, check := range []struct {
			actual, target int64
			label          string
		}{
			{run.Powder, plan.Powder, "powder"},
			{run.Water, plan.Water, "water"},
			{run.Admixture, plan.Admixture, "admixture"},
		} {
			if err := withinDeviation(check.actual, check.target, rule.AllowDeviation); err != nil {
				return codes.New(codes.CodeMixInvalid, fmt.Sprintf("%s dosage out of tolerance: %v", check.label, err))
			}
		}
		if run.Fiber != plan.Fiber {
			return codes.New(codes.CodeMixInvalid, "fiber dosage is not conserved")
		}

		seq := st.nextSequence()
		id := fmt.Sprintf("gen-%d", seq)
		gen = domain.MaterialGeneration{
			ID:          id,
			JointNumber: jointID,
			Batch:       run.Batch,
			Deadline:    run.Time + domain.LogicalTime(rule.WorkWindow),
			Valid:       true,
		}
		st.Generations[id] = gen
		j.Generation = id
		j.MixCount++
		st.advanceNow(run.Time)
		return nil
	})
	return gen, err
}

// RecordFlow records a flow test result that gates pouring authorization. A
// value outside the recipe window or a failed test never authorizes pouring.
func (e *Engine) RecordFlow(jointID string, flow domain.FlowTest) error {
	var flowMin, flowMax fixedpoint.Value
	err := e.mutate("record-flow", func(st *state) error {
		j, ok := st.Joints[jointID]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}
		rule, ok := st.Recipes[j.Design.Recipe]
		if !ok {
			return codes.New(codes.CodeSpanRecipeMismatch, "recipe not registered")
		}
		flowMin, flowMax = rule.FlowMin, rule.FlowMax
		j.Flow = &flow
		st.advanceNow(flow.Time)
		st.nextSequence()
		return nil
	})
	if err != nil {
		return err
	}
	if !flow.Passed {
		return codes.New(codes.CodeFlowFailed, "flow test did not pass")
	}
	inWindow, err := flowInWindow(flow.Value, flowMin, flowMax)
	if err != nil {
		return codes.New(codes.CodeFlowFailed, "flow value invalid: "+err.Error())
	}
	if !inWindow {
		return codes.New(codes.CodeFlowFailed, "flow value outside recipe window")
	}
	return nil
}

// RecordInspection appends inspection evidence. Evidence for a stale generation
// is archived without affecting the current conclusion.
func (e *Engine) RecordInspection(jointID string, ev domain.InspectionEvidence) error {
	return e.mutate("inspection", func(st *state) error {
		j, ok := st.Joints[jointID]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}
		if !validInspectionKind(ev.Kind) {
			return codes.New(codes.CodeInspectionFailed, "unknown inspection kind "+ev.Kind)
		}
		if ev.Generation != j.Generation {
			j.Archived = append(j.Archived, ev)
			st.advanceNow(ev.Time)
			st.nextSequence()
			return nil
		}
		j.Inspections = append(j.Inspections, ev)
		recheckRetest(st, j)
		st.advanceNow(ev.Time)
		st.nextSequence()
		return nil
	})
}

// recheckRetest marks the active retest complete once every affected segment
// has a passing inspection (of the anomaly kind) in the current generation.
func recheckRetest(st *state, j *jointState) {
	if j.RetestID == "" {
		return
	}
	rs, ok := st.Retests[j.RetestID]
	if !ok {
		return
	}
	for _, seg := range rs.Segments {
		if !hasPassedInspectionFor(j, rs.Anomaly.Kind, seg) {
			return
		}
	}
	rs.Done = true
	st.Retests[j.RetestID] = rs
	if rem, ok := st.Remediations[j.RetestID]; ok {
		rem.Complete = true
		st.Remediations[j.RetestID] = rem
	}
}

func hasPassedInspectionFor(j *jointState, kind string, seg int) bool {
	for _, ev := range j.Inspections {
		if ev.Kind == kind && ev.Segment == seg && ev.Passed {
			return true
		}
	}
	return false
}

// validInspectionKind reports whether kind is one of the documented inspection
// kinds (strength, pull-off, shrinkage, crack, appearance).
func validInspectionKind(kind string) bool {
	switch kind {
	case KindStrength, KindPullOff, KindShrinkage, KindCrack, KindAppearance:
		return true
	}
	return false
}

// surfaceConfirmed reports whether every required surface zone is confirmed.
func surfaceConfirmed(j *jointState) (bool, error) {
	if j == nil {
		return false, codes.New(codes.CodeJointNotFound, "joint not found")
	}
	for _, z := range j.Design.SurfaceZones {
		if !z.Required {
			continue
		}
		ev, ok := j.Surface[z.ID]
		if !ok || !ev.Clean || !ev.PreWet {
			return false, nil
		}
	}
	return true, nil
}

func planFor(plans []domain.MixPlan, seq int) (domain.MixPlan, error) {
	for _, p := range plans {
		if p.Sequence == seq {
			return p, nil
		}
	}
	return domain.MixPlan{}, codes.New(codes.CodeMixInvalid, "no plan for sequence")
}

// withinDeviation checks that actual lies within target ± (target*deviation),
// using overflow-checked fixed-point ratio multiplication.
func withinDeviation(actual, target int64, dev fixedpoint.Value) error {
	if target < 0 {
		return codes.New(codes.CodeGeometryNegative, "negative target dose")
	}
	tol, err := dev.MulInt(target)
	if err != nil {
		return err
	}
	if actual < target-tol || actual > target+tol {
		return fmt.Errorf("actual %d outside [%d,%d]", actual, target-tol, target+tol)
	}
	return nil
}

func flowInWindow(v, lo, hi fixedpoint.Value) (bool, error) {
	scaled, err := v.Rescale(lo.Scale())
	if err != nil {
		return false, err
	}
	if scaled.Raw() < lo.Raw() {
		return false, nil
	}
	scaledHi, err := v.Rescale(hi.Scale())
	if err != nil {
		return false, err
	}
	return scaledHi.Raw() <= hi.Raw(), nil
}
