package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
)

// Inspection kinds.
const (
	KindStrength   = "STRENGTH"
	KindPullOff    = "PULL_OFF"
	KindShrinkage  = "SHRINKAGE"
	KindCrack      = "CRACK"
	KindAppearance = "APPEARANCE"
)

// Retest computes the unique ordered retest closure for an anomaly: the
// anomaly's segment plus, transitively, adjacent segments, same-generation
// segments, and segments sharing a base surface zone. The same anomaly fact
// yields exactly one set.
func (e *Engine) Retest(anomaly domain.Anomaly) (domain.RetestSet, error) {
	var set domain.RetestSet
	err := e.mutate("retest", func(st *state) error {
		j, ok := st.Joints[anomaly.JointNumber]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+anomaly.JointNumber+" not found")
		}

		id := retestID(anomaly)
		if existing, ok := st.Retests[id]; ok {
			set = existing
			return nil
		}

		segs := retestClosure(j, anomaly.Segment, anomaly.Generation)
		set = domain.RetestSet{
			ID:       id,
			Anomaly:  anomaly,
			Segments: segs,
			Done:     false,
		}
		st.Retests[id] = set
		j.RetestID = id
		st.nextSequence()
		return nil
	})
	return set, err
}

// ActivateGeneration activates a remediation generation over a retest set,
// replacing the joint's active generation so stale evidence is archived.
func (e *Engine) ActivateGeneration(retestID string) (domain.RemediationGeneration, error) {
	var rem domain.RemediationGeneration
	err := e.mutate("activate-generation", func(st *state) error {
		rs, ok := st.Retests[retestID]
		if !ok {
			return codes.New(codes.CodeRetestNotFound, "retest "+retestID+" not found")
		}
		j, ok := st.Joints[rs.Anomaly.JointNumber]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint not found")
		}
		seq := st.nextSequence()
		genID := fmt.Sprintf("gen-%d", seq)
		rem = domain.RemediationGeneration{
			ID:       genID,
			RetestID: retestID,
			Previous: j.Generation,
			Segments: append([]int(nil), rs.Segments...),
			Complete: false,
		}
		st.Remediations[retestID] = rem
		j.Generation = genID
		return nil
	})
	return rem, err
}

// retestID derives a deterministic, unique ID for an anomaly fact.
func retestID(a domain.Anomaly) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%s", a.JointNumber, a.Kind, a.Segment, a.Generation)))
	return "retest-" + hex.EncodeToString(h[:8])
}

// retestClosure computes the transitive closure of an anomaly's influence over
// adjacency, same-generation fills, and shared base surface zones.
func retestClosure(j *jointState, seed int, generation string) []int {
	set := map[int]bool{seed: true}
	for {
		changed := false
		// Adjacency propagation.
		for s := range set {
			if s >= 0 && s < len(j.Design.Adjacency) {
				for _, n := range j.Design.Adjacency[s] {
					if !set[n] {
						set[n] = true
						changed = true
					}
				}
			}
		}
		// Same-generation fill propagation.
		for _, f := range j.Fills {
			if f.Generation == generation && !set[f.Segment] {
				set[f.Segment] = true
				changed = true
			}
		}
		// Shared surface-zone propagation.
		for s := range set {
			for other := range j.Design.Geometry.Segments {
				if other == s {
					continue
				}
				if shareZone(j.Design.SegmentZones[s], j.Design.SegmentZones[other]) && !set[other] {
					set[other] = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	out := make([]int, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Ints(out)
	return out
}

func shareZone(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
