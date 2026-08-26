package engine

import (
	"fmt"
	"sort"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/geometry"
)

// PourEquipment is the equipment category required for an append-fill write.
const PourEquipment = "POUR"

type cellCoord struct {
	seg   int
	layer int
}

// pourOrder computes the locked pour order: layer-major, segments in the locked
// direction (ascending or descending by start coordinate).
func pourOrder(d domain.JointDesign) []cellCoord {
	segs := append([]geometry.Segment(nil), d.Geometry.Segments...)
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
	if d.Geometry.Direction == geometry.DirectionDescending {
		for i, j := 0, len(segs)-1; i < j; i, j = i+1, j-1 {
			segs[i], segs[j] = segs[j], segs[i]
		}
	}
	var order []cellCoord
	for layer := 0; layer < d.Geometry.Layers; layer++ {
		for _, s := range segs {
			order = append(order, cellCoord{seg: s.Index, layer: layer})
		}
	}
	return order
}

// prefixFromFills derives the continuous pour prefix from the append-only fill
// list and the locked pour order.
func prefixFromFills(j *jointState) domain.PourPrefix {
	order := pourOrder(j.Design)
	n := len(j.Fills)
	if n >= len(order) {
		last := order[len(order)-1]
		return domain.PourPrefix{Segment: last.seg, Layer: last.layer, Done: true}
	}
	if n == 0 {
		return domain.PourPrefix{Segment: order[0].seg, Layer: order[0].layer, Done: false}
	}
	next := order[n]
	return domain.PourPrefix{Segment: next.seg, Layer: next.layer, Done: false}
}

// RecordSurfaceEvidence appends base-surface evidence for one zone.
func (e *Engine) RecordSurfaceEvidence(jointID string, ev domain.SurfaceEvidence) error {
	return e.mutate("surface-evidence", func(st *state) error {
		j, ok := st.Joints[jointID]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}
		if _, exists := j.Surface[ev.ZoneID]; exists {
			return codes.New(codes.CodeZoneAlreadyRecorded, "zone "+ev.ZoneID+" already recorded")
		}
		if !zoneExists(j.Design.SurfaceZones, ev.ZoneID) {
			return codes.New(codes.CodeJointNotFound, "unknown zone "+ev.ZoneID)
		}
		j.Surface[ev.ZoneID] = ev
		st.advanceNow(ev.Time)
		st.nextSequence()
		return nil
	})
}

// SurfaceConfirmed reports whether every required zone is confirmed.
func (e *Engine) SurfaceConfirmed(jointID string) (bool, error) {
	var ok bool
	err := e.read(func(st *state) error {
		j, exists := st.Joints[jointID]
		if !exists {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}
		var err error
		ok, err = surfaceConfirmed(j)
		return err
	})
	return ok, err
}

// AppendFill appends the next fill cell and advances the continuous prefix by
// exactly one cell. It enforces surface confirmation, flow authorization, a
// valid and unexpired material generation, an active pouring lease, and strict
// prefix ordering.
func (e *Engine) AppendFill(jointID string, cell domain.FillCell) (domain.PourPrefix, error) {
	var prefix domain.PourPrefix
	err := e.mutate("append-fill", func(st *state) error {
		j, ok := st.Joints[jointID]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}

		order := pourOrder(j.Design)
		if len(j.Fills) >= len(order) {
			return codes.New(codes.CodePourDuplicate, "pour prefix already complete")
		}
		expected := order[len(j.Fills)]
		if cell.Segment != expected.seg || cell.Layer != expected.layer {
			return codes.New(codes.CodePourOutOfOrder,
				fmt.Sprintf("expected segment %d layer %d, got segment %d layer %d",
					expected.seg, expected.layer, cell.Segment, cell.Layer))
		}

		if confirmed, err := surfaceConfirmed(j); err != nil {
			return err
		} else if !confirmed {
			return codes.New(codes.CodeSurfaceNotConfirmed, "surface handover is not confirmed")
		}
		if j.Flow == nil {
			return codes.New(codes.CodeFlowMissing, "no passing flow test recorded")
		}

		// Material generation: must be the active, valid, unexpired generation.
		gen, ok := st.Generations[cell.Generation]
		if !ok || gen.ID != j.Generation {
			return codes.New(codes.CodeStaleGeneration, "fill references a non-active generation")
		}
		if cell.Time >= gen.Deadline {
			return codes.New(codes.CodeMaterialExpired, "material generation has expired")
		}
		if cell.MixBatch != gen.Batch {
			return codes.New(codes.CodePourWrongBatch, "fill batch does not match generation")
		}

		// Logical time must not move backwards relative to prior fills.
		if n := len(j.Fills); n > 0 && cell.Time < j.Fills[n-1].Time {
			return codes.New(codes.CodePourTimeBackward, "fill logical time moved backwards")
		}

		// A valid pouring lease must cover this write.
		if !hasActiveLease(st, PourEquipment, cell.Time) {
			return codes.New(codes.CodeLeaseExpired, "no active pouring lease")
		}

		j.Fills = append(j.Fills, cell)
		prefix = prefixFromFills(j)
		st.advanceNow(cell.Time)
		st.nextSequence()
		return nil
	})
	return prefix, err
}

// Prefix returns the current continuous pour prefix.
func (e *Engine) Prefix(jointID string) (domain.PourPrefix, error) {
	var prefix domain.PourPrefix
	err := e.read(func(st *state) error {
		j, ok := st.Joints[jointID]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}
		prefix = prefixFromFills(j)
		return nil
	})
	return prefix, err
}

// RecordCuring appends a curing sample. Once the cumulative duration reaches the
// locked schedule, the curing timeline is closed.
func (e *Engine) RecordCuring(jointID string, ev domain.CuringEvidence) error {
	return e.mutate("record-curing", func(st *state) error {
		j, ok := st.Joints[jointID]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}
		if j.CuringClosed {
			return codes.New(codes.CodeCuringOutOfOrder, "curing timeline already closed")
		}
		if !prefixFromFills(j).Done {
			return codes.New(codes.CodeCuringOutOfOrder, "curing requires a complete pour prefix")
		}
		if n := len(j.Curing); n > 0 && ev.Time < j.Curing[n-1].Time {
			return codes.New(codes.CodeCuringOutOfOrder, "curing logical time moved backwards")
		}
		j.Curing = append(j.Curing, ev)
		if curingDuration(j) >= j.Design.Curing.DurationMinutes {
			j.CuringClosed = true
		}
		st.advanceNow(ev.Time)
		st.nextSequence()
		return nil
	})
}

// CuringClosed reports whether the curing timeline is closed.
func (e *Engine) CuringClosed(jointID string) (bool, error) {
	var closed bool
	err := e.read(func(st *state) error {
		j, ok := st.Joints[jointID]
		if !ok {
			return codes.New(codes.CodeJointNotFound, "joint "+jointID+" not found")
		}
		closed = j.CuringClosed
		return nil
	})
	return closed, err
}

func zoneExists(zones []domain.SurfaceZone, id string) bool {
	for _, z := range zones {
		if z.ID == id {
			return true
		}
	}
	return false
}

func curingDuration(j *jointState) int64 {
	var total int64
	for _, s := range j.Curing {
		total += s.Duration
	}
	return total
}

// hasActiveLease reports whether any active lease of the given category covers
// the logical time t (start <= t < deadline).
func hasActiveLease(st *state, category string, t domain.LogicalTime) bool {
	for _, lease := range st.Leases {
		if lease.Active && lease.Category == category && lease.Start <= t && t < lease.Deadline {
			return true
		}
	}
	return false
}
