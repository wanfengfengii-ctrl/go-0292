package engine

import (
	"fmt"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/fixedpoint"
)

// SubmitReview records one independent qualified reviewer conclusion.
func (e *Engine) SubmitReview(jointID string, review domain.Review) error {
	return e.mutate("review", func(st *state) error {
		if _, ok := st.Joints[jointID]; !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}
		if !review.Qualified {
			return codes.New(codes.CodeNotQualified, "reviewer "+review.Reviewer+" is not qualified")
		}
		for _, r := range st.Reviews[jointID] {
			if r.Reviewer == review.Reviewer {
				return codes.New(codes.CodeDuplicateReviewer, "reviewer "+review.Reviewer+" already submitted")
			}
		}
		st.Reviews[jointID] = append(st.Reviews[jointID], review)
		st.nextSequence()
		return nil
	})
}

// Verdict executes the single-writer final verdict barrier. Only one verdict per
// joint may ever be committed; a RELEASE additionally requires all quality
// gates to be satisfied and generates a unique traffic credential.
func (e *Engine) Verdict(jointID string, verdict domain.FinalVerdict) (domain.FinalVerdict, error) {
	var result domain.FinalVerdict
	err := e.mutate("verdict", func(st *state) error {
		j, ok := st.Joints[jointID]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}
		if _, exists := st.Verdicts[jointID]; exists {
			return codes.New(codes.CodeVerdictConflict, "a final verdict already exists for "+jointID)
		}

		if verdict.Type == domain.VerdictRelease {
			if err := verifyReleasePreconditions(st, j, st.Reviews[jointID]); err != nil {
				return err
			}
			seq := st.nextSequence()
			verdict.Credential = fmt.Sprintf("cred-%d", seq)
		} else {
			st.nextSequence()
		}
		verdict.BarrierVersion = len(st.Verdicts) + 1
		verdict.JointNumber = jointID
		st.Verdicts[jointID] = verdict
		result = verdict
		return nil
	})
	return result, err
}

// verifyReleasePreconditions returns the first unmet traffic-release gate.
func verifyReleasePreconditions(st *state, j *jointState, reviews []domain.Review) error {
	if !prefixFromFills(j).Done {
		return codes.New(codes.CodePreconditionsNotMet, "pour prefix is not complete")
	}
	if !j.CuringClosed {
		return codes.New(codes.CodePreconditionsNotMet, "curing timeline is not closed")
	}
	rule, ok := st.Recipes[j.Design.Recipe]
	if !ok {
		return codes.New(codes.CodeSpanRecipeMismatch, "recipe not registered")
	}

	if !hasPassed(j, KindStrength, rule.MinStrength, geThreshold) {
		return codes.New(codes.CodePreconditionsNotMet, "no passing strength inspection")
	}
	if !hasPassed(j, KindPullOff, rule.MinBondStrength, geThreshold) {
		return codes.New(codes.CodePreconditionsNotMet, "no passing pull-off inspection")
	}
	if hasFailed(j) {
		return codes.New(codes.CodePreconditionsNotMet, "a current-generation inspection failed")
	}
	if j.RetestID != "" {
		if rs, ok := st.Retests[j.RetestID]; !ok || !rs.Done {
			return codes.New(codes.CodePreconditionsNotMet, "retest set is not complete")
		}
	}
	if !twoAgreeingQualified(reviews) {
		return codes.New(codes.CodePreconditionsNotMet, "two agreeing qualified reviewers required")
	}
	return nil
}

// thresholdCmp compares a reading against a threshold after rescaling.
type thresholdCmp func(reading, threshold fixedpoint.Value) bool

func geThreshold(reading, threshold fixedpoint.Value) bool { return compareGE(reading, threshold) }

// hasPassed reports whether a current-generation inspection of the given kind
// both passed and satisfies the fixed-point threshold.
func hasPassed(j *jointState, kind string, threshold fixedpoint.Value, cmp thresholdCmp) bool {
	for _, ev := range j.Inspections {
		if ev.Kind == kind && ev.Passed && cmp(ev.Reading, threshold) {
			return true
		}
	}
	return false
}

func hasFailed(j *jointState) bool {
	for _, ev := range j.Inspections {
		if !ev.Passed {
			return true
		}
	}
	return false
}

// twoAgreeingQualified reports whether at least two distinct qualified
// reviewers submitted the same conclusion.
func twoAgreeingQualified(reviews []domain.Review) bool {
	byConclusion := make(map[string]map[string]bool)
	for _, r := range reviews {
		if !r.Qualified {
			continue
		}
		if byConclusion[r.Conclusion] == nil {
			byConclusion[r.Conclusion] = make(map[string]bool)
		}
		byConclusion[r.Conclusion][r.Reviewer] = true
		if len(byConclusion[r.Conclusion]) >= 2 {
			return true
		}
	}
	return false
}

// compareGE reports whether reading >= threshold after rescaling to a common
// scale. On any error (overflow, scale mismatch) it reports false.
func compareGE(reading, threshold fixedpoint.Value) bool {
	r, err := reading.Rescale(threshold.Scale())
	if err != nil {
		return false
	}
	return r.Raw() >= threshold.Raw()
}
