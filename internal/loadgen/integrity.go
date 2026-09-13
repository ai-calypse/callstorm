package loadgen

// Integrity is whether Callstorm's own event pipeline delivered what it sent.
//
// It checks the harness, not the agent under test. Callstorm does not receive
// the target's webhooks, so nothing here says whether a CRM got its record.
// What it does say is whether the numbers on this report rest on every call
// the run placed: a result that was lost, or counted twice, changes a step's
// percentiles as surely as the agent does.
type Integrity struct {
	// Dispatch is present on a distributed run, where every call travels to a
	// worker and back over Kafka.
	Dispatch *DispatchIntegrity `json:"dispatch,omitempty"`

	// TurnEvents is present when per-turn events were published to Kafka.
	TurnEvents *TurnEventIntegrity `json:"turn_events,omitempty"`
}

// DispatchIntegrity totals the dispatch pipeline over a run.
type DispatchIntegrity struct {
	Dispatched int `json:"dispatched"`

	// Received counts distinct calls that came back, one per (step, seq).
	Received int `json:"received"`

	// Duplicates counts results for a call already received. Delivery is
	// at-least-once by design -- a worker killed between publishing and
	// committing hands its call to another -- so a duplicate is expected
	// under chaos. It is dropped from the step rather than counted twice.
	Duplicates int `json:"duplicates"`

	// Missing counts calls dispatched whose result never arrived before the
	// step gave up waiting. They are scored as failed calls.
	Missing int `json:"missing"`

	// Late counts results that arrived after their own step had closed. A
	// result still in flight when the run ends is not seen at all.
	Late int `json:"late"`

	Steps []StepDispatch `json:"steps"`
}

// StepDispatch is the same count for one step.
type StepDispatch struct {
	Step       string `json:"step"`
	Dispatched int    `json:"dispatched"`
	Received   int    `json:"received"`
	Duplicates int    `json:"duplicates"`
	Missing    int    `json:"missing"`

	// Late counts results from earlier steps that arrived while this one ran.
	Late int `json:"late"`
}

func (d *DispatchIntegrity) add(s StepDispatch) {
	d.Dispatched += s.Dispatched
	d.Received += s.Received
	d.Duplicates += s.Duplicates
	d.Missing += s.Missing
	d.Late += s.Late
	d.Steps = append(d.Steps, s)
}

// TurnEventIntegrity is what the fire-and-forget turn event producer managed.
type TurnEventIntegrity struct {
	// Published counts events the broker acknowledged; Dropped counts produce
	// failures. Produces never block a caller, so a drop is counted rather
	// than retried.
	Published int64  `json:"published"`
	Dropped   int64  `json:"dropped"`
	LastError string `json:"last_error,omitempty"`
}
