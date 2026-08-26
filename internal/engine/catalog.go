package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"example.com/uhpc-wet-joint-traffic-release/internal/codes"
	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/geometry"
)

// RegisterSpan records a bridge span with its coordinate scale, allowed recipes
// and current rule digest.
func (e *Engine) RegisterSpan(span domain.BridgeSpan) error {
	return e.mutate("register-span", func(st *state) error {
		if _, exists := st.Spans[span.ID]; exists {
			return codes.New(codes.CodeAlreadyLocked, "span "+span.ID+" already registered")
		}
		if span.CoordinateScale <= 0 {
			return codes.New(codes.CodeGeometryNegative, "span coordinate scale must be positive")
		}
		if len(span.AllowedRecipes) == 0 {
			return codes.New(codes.CodeSpanRecipeMismatch, "span must allow at least one recipe")
		}
		st.Spans[span.ID] = span
		st.nextSequence()
		return nil
	})
}

// RegisterRecipe records a UHPC recipe revision and bumps the global rule
// version so stale lock attempts are rejected.
func (e *Engine) RegisterRecipe(rule domain.RecipeRule) error {
	return e.mutate("register-recipe", func(st *state) error {
		if rule.Name == "" {
			return codes.New(codes.CodeMixInvalid, "recipe name must not be empty")
		}
		if rule.WorkWindow <= 0 {
			return codes.New(codes.CodeMixInvalid, "recipe work window must be positive")
		}
		st.Recipes[rule.Name] = rule
		st.RuleVersion++
		st.nextSequence()
		return nil
	})
}

// Lock validates and freezes a joint design into an immutable summary. It
// rejects invalid geometry, span/recipe mismatch, and stale rule digests.
func (e *Engine) Lock(jointID string, design domain.JointDesign) (domain.LockSummary, error) {
	var summary domain.LockSummary
	err := e.mutate("lock", func(st *state) error {
		if _, exists := st.Joints[jointID]; exists {
			return codes.New(codes.CodeAlreadyLocked, "joint "+jointID+" is already locked")
		}

		span, ok := st.Spans[design.SpanID]
		if !ok {
			return codes.New(codes.CodeSpanRecipeMismatch, "unknown span "+design.SpanID)
		}

		// Geometry validation first: deterministic, ordered reasons.
		if errs := design.Geometry.Validate(); len(errs) > 0 {
			return geometryFirstError(errs)
		}

		// Span/recipe matching.
		if !contains(span.AllowedRecipes, design.Recipe) {
			return codes.New(codes.CodeSpanRecipeMismatch,
				fmt.Sprintf("recipe %s is not allowed for span %s", design.Recipe, design.SpanID))
		}
		if _, ok := st.Recipes[design.Recipe]; !ok {
			return codes.New(codes.CodeSpanRecipeMismatch, "recipe "+design.Recipe+" is not registered")
		}

		// Rule freshness: the design must target the current rule version.
		if design.LockVersion != st.RuleVersion {
			return codes.New(codes.CodeStaleRuleDigest,
				fmt.Sprintf("design rule version %d is stale (current %d)", design.LockVersion, st.RuleVersion))
		}

		// Mix plan sanity: sequences must be contiguous and doses non-negative.
		if err := validateMixPlans(design.MixPlans, design.Geometry.Layers); err != nil {
			return err
		}

		digest := designDigest(design)
		summary = domain.LockSummary{
			JointNumber: jointID,
			Version:     st.RuleVersion,
			RuleDigest:  span.RuleDigest,
			Digest:      digest,
		}

		st.Joints[jointID] = &jointState{
			SpanID:  design.SpanID,
			Design:  design,
			Summary: summary,
			Surface: make(map[string]domain.SurfaceEvidence),
		}
		st.nextSequence()
		return nil
	})
	return summary, err
}

// geometryFirstError converts the ordered geometry validation errors into a
// single stable domain error (the first by code then message).
func geometryFirstError(errs []geometry.ValidationError) error {
	first := errs[0]
	return codes.New(first.Code, first.Message)
}

// validateMixPlans asserts that mix plan sequences are contiguous starting at 0
// and that every component dose is non-negative.
func validateMixPlans(plans []domain.MixPlan, layers int) error {
	if len(plans) == 0 {
		return codes.New(codes.CodeMixInvalid, "no mix plans defined")
	}
	seqs := make([]int, 0, len(plans))
	seen := make(map[int]bool)
	for _, p := range plans {
		if p.Powder < 0 || p.Water < 0 || p.Admixture < 0 || p.Fiber < 0 {
			return codes.New(codes.CodeGeometryNegative, "mix plan doses must be non-negative")
		}
		if seen[p.Sequence] {
			return codes.New(codes.CodeMixInvalid, "duplicate mix plan sequence")
		}
		seen[p.Sequence] = true
		seqs = append(seqs, p.Sequence)
	}
	sort.Ints(seqs)
	for i, s := range seqs {
		if s != i {
			return codes.New(codes.CodeMixInvalid, "mix plan sequences must be contiguous from 0")
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// designDigest computes an immutable content digest of a design.
func designDigest(d domain.JointDesign) string {
	raw, _ := json.Marshal(d)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
