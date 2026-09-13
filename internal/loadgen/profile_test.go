package loadgen

import "testing"

func TestValidateRejectsUnknownKind(t *testing.T) {
	p := Profile{Steps: []Step{{Name: "c1", Concurrency: 1, Kind: "burst"}}}
	err := p.validate()
	if err == nil {
		t.Fatal("accepted an unknown step kind")
	}
	if got := err.Error(); got == "" || !contains(got, "burst") {
		t.Errorf("error %q does not name the kind that was wrong", got)
	}
}

func TestValidateRejectsBothBudgets(t *testing.T) {
	p := Profile{Steps: []Step{{Name: "soak", Concurrency: 2, Calls: 10, HoldSeconds: 60}}}
	if err := p.validate(); err == nil {
		t.Fatal("accepted a step with two stopping conditions")
	}
}

func TestValidateLeavesHeldStepsWithoutACallBudget(t *testing.T) {
	// A held step must not have Calls defaulted to Concurrency behind its
	// back: that would give it a call budget it would then hit before the
	// clock, and the soak would quietly end in seconds.
	p := Profile{Steps: []Step{{Name: "soak", Concurrency: 4, HoldSeconds: 60}}}
	if err := p.validate(); err != nil {
		t.Fatalf("rejected a valid held step: %v", err)
	}
	if p.Steps[0].Calls != 0 {
		t.Errorf("held step was given a budget of %d calls", p.Steps[0].Calls)
	}
}

func TestValidateRejectsRecoveryAtTheWrongConcurrency(t *testing.T) {
	p := Profile{Baseline: "base", Steps: []Step{
		{Name: "base", Concurrency: 4, Calls: 8},
		{Name: "stress", Concurrency: 16, Calls: 32},
		{Name: "back", Concurrency: 8, Calls: 8, Kind: KindRecovery},
	}}
	err := p.validate()
	if err == nil {
		t.Fatal("accepted a recovery step at a concurrency the baseline never ran at")
	}
	if !contains(err.Error(), "recovery") {
		t.Errorf("error %q does not say what is wrong", err)
	}
}

func TestValidateRejectsRecoveryWithNothingToRecoverFrom(t *testing.T) {
	p := Profile{Baseline: "base", Steps: []Step{
		{Name: "base", Concurrency: 4, Calls: 8},
		{Name: "back", Concurrency: 4, Calls: 8, Kind: KindRecovery},
	}}
	if err := p.validate(); err == nil {
		t.Fatal("accepted a recovery that follows nothing heavier than the baseline")
	}
}

func TestValidateAcceptsAWholeHammingProfile(t *testing.T) {
	p := Profile{Baseline: "ramp", Steps: []Step{
		{Name: "smoke", Concurrency: 1, Calls: 2, Kind: KindSmoke},
		{Name: "ramp", Concurrency: 4, Calls: 16, Kind: KindRamp},
		{Name: "stress", Concurrency: 12, Calls: 36, Kind: KindStress},
		{Name: "spike", Concurrency: 24, Calls: 48, Kind: KindSpike},
		{Name: "soak", Concurrency: 8, HoldSeconds: 120, Kind: KindSoak},
		{Name: "recover", Concurrency: 4, Calls: 16, Kind: KindRecovery},
	}}
	if err := p.validate(); err != nil {
		t.Fatalf("rejected the six-phase profile: %v", err)
	}
	if got := p.TotalCalls(); got != 118 {
		t.Errorf("TotalCalls = %d, want 118: a held step contributes no known calls", got)
	}
	if got := p.HeldSeconds(); got != 120 {
		t.Errorf("HeldSeconds = %v, want 120", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
