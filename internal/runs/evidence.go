package runs

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Criterion statuses in evidence YAML.
const (
	CriterionPass    = "pass"
	CriterionPartial = "partial"
	CriterionFail    = "fail"
)

// Evidence is `.djinn/evidence/<bead>.yml`, the per-criterion record a djinn
// worker leaves behind.
//
// The schema is taken from real files in ~/Projects/djinn/.djinn/evidence.
// Parsing is deliberately tolerant: unknown fields are ignored and every
// optional is a pointer or a nil-able slice, because these files are written by
// an LLM and the shape drifts.
type Evidence struct {
	Bead        string      `yaml:"bead" json:"bead"`
	Title       string      `yaml:"title" json:"title"`
	Description string      `yaml:"description" json:"description,omitempty"`
	Criteria    []Criterion `yaml:"criteria" json:"criteria"`
	// FollowUp lists bead ids filed as follow-ups. Real files carry either an
	// empty list or a list of ids.
	FollowUp []string `yaml:"follow_up" json:"followUp"`
}

// Criterion is one acceptance criterion's verdict.
type Criterion struct {
	Text   string `yaml:"text" json:"text"`
	Status string `yaml:"status" json:"status"`
	// NonCode marks a criterion that cannot be satisfied by code alone.
	NonCode bool `yaml:"non_code" json:"nonCode"`
	// Reason explains a partial or failed criterion.
	Reason string `yaml:"reason" json:"reason,omitempty"`
	// Implementation is a list of `path:line` references.
	Implementation []string `yaml:"implementation" json:"implementation"`
	// Verification is the command or procedure that proves the criterion.
	Verification string         `yaml:"verification" json:"verification,omitempty"`
	Evidence     CriterionProof `yaml:"evidence" json:"evidence"`
	FollowUp     []string       `yaml:"follow_up" json:"followUp,omitempty"`
}

// CriterionProof is the `evidence:` block under a criterion.
type CriterionProof struct {
	// Snapshots are screenshot or artifact paths. The schema reserves this;
	// no file in the current corpus populates it.
	Snapshots []string `yaml:"snapshots" json:"snapshots"`
	// Observations are free-text proof lines, present on nearly every real
	// criterion.
	Observations []string `yaml:"observations" json:"observations"`
}

// Passed reports whether every criterion passed.
func (e *Evidence) Passed() bool {
	if e == nil || len(e.Criteria) == 0 {
		return false
	}
	for _, c := range e.Criteria {
		if c.Status != CriterionPass {
			return false
		}
	}
	return true
}

// Counts returns how many criteria hold each status.
func (e *Evidence) Counts() (pass, partial, fail, other int) {
	if e == nil {
		return 0, 0, 0, 0
	}
	for _, c := range e.Criteria {
		switch c.Status {
		case CriterionPass:
			pass++
		case CriterionPartial:
			partial++
		case CriterionFail:
			fail++
		default:
			other++
		}
	}
	return pass, partial, fail, other
}

// ParseEvidence decodes evidence YAML.
func ParseEvidence(data []byte) (*Evidence, error) {
	var e Evidence
	if err := yaml.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("parse evidence: %w", err)
	}
	normalizeEvidence(&e)
	return &e, nil
}

// normalizeEvidence guarantees non-nil slices so the JSON contract is stable:
// the UI should render an empty list, never null.
func normalizeEvidence(e *Evidence) {
	if e.FollowUp == nil {
		e.FollowUp = []string{}
	}
	if e.Criteria == nil {
		e.Criteria = []Criterion{}
	}
	for i := range e.Criteria {
		c := &e.Criteria[i]
		if c.Implementation == nil {
			c.Implementation = []string{}
		}
		if c.Evidence.Observations == nil {
			c.Evidence.Observations = []string{}
		}
		if c.Evidence.Snapshots == nil {
			c.Evidence.Snapshots = []string{}
		}
	}
}

// ReadEvidence loads `<djinnDir>/evidence/<bead>.yml`.
//
// A missing file returns (nil, nil): most beads have no evidence, and that is
// not an error the caller should have to distinguish.
func ReadEvidence(djinnDir, bead string) (*Evidence, error) {
	if !ValidID(bead) {
		return nil, fmt.Errorf("invalid bead id %q", bead)
	}
	path := filepath.Join(djinnDir, "evidence", bead+".yml")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read evidence for %s: %w", bead, err)
	}
	return ParseEvidence(data)
}
