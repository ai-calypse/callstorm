package loadgen

// NodeStats is how one scenario node fared over one step.
//
// A call-level pass rate says the agent got worse under load and nothing about
// where. Completion can hold at 70% while the one node that verifies payment
// has collapsed to 12%, and that is a different fix from a slow greeting. The
// per-node rate is what pairs with the saturation curve: the curve says when,
// this says which part of the conversation gave way.
type NodeStats struct {
	Node string `json:"node"`

	// Visits counts turns that spoke this node. In a graph a node can be
	// visited twice in one call, or never, so visits are not calls.
	Visits int `json:"visits"`

	// Checked counts visits whose node carries an assertion, and Passed the
	// ones where the agent's reply satisfied it. PassRate is Passed over
	// Checked; a node with no assertion has no rate, not a perfect one.
	Checked  int     `json:"checked"`
	Passed   int     `json:"passed"`
	PassRate float64 `json:"pass_rate"`

	// Miss is the first reason a visit failed, as evidence. A rate says how
	// often; this says what it looked like.
	Miss string `json:"miss,omitempty"`
}

// summarizeNodes tallies every node over one step's calls, in the scenario's
// own order so the same node sits in the same row of every step.
//
// Nodes the step never reached are kept with zero visits. In a graph an
// unvisited node is a finding -- the branch leading to it never fired -- and
// dropping the row would hide it.
func summarizeNodes(outcomes []callOutcome, order []string) []NodeStats {
	by := map[string]*NodeStats{}
	var extra []string
	for _, o := range outcomes {
		if o.err != nil {
			continue
		}
		for _, t := range o.turns {
			if t.Node == "" {
				continue
			}
			ns, ok := by[t.Node]
			if !ok {
				ns = &NodeStats{Node: t.Node}
				by[t.Node] = ns
				extra = append(extra, t.Node)
			}
			ns.Visits++
			if !t.ExpectChecked {
				continue
			}
			ns.Checked++
			if t.ExpectMet {
				ns.Passed++
			} else if ns.Miss == "" {
				ns.Miss = t.ExpectMiss
			}
		}
	}
	// A run recorded before nodes existed, or a step where nothing connected,
	// has nothing to report per node.
	if len(by) == 0 {
		return nil
	}

	out := make([]NodeStats, 0, len(order))
	placed := map[string]bool{}
	for _, id := range order {
		ns := by[id]
		if ns == nil {
			ns = &NodeStats{Node: id}
		}
		out = append(out, *ns)
		placed[id] = true
	}
	// A node the scenario no longer names, in a run replayed against an edited
	// file, still happened, so it is reported rather than dropped.
	for _, id := range extra {
		if !placed[id] {
			out = append(out, *by[id])
		}
	}
	for i := range out {
		if out[i].Checked > 0 {
			out[i].PassRate = round3(float64(out[i].Passed) / float64(out[i].Checked))
		}
	}
	return out
}
