package tunnel

import (
	"math"
	"testing"
	"time"
)

func TestRetryPolicy_AttemptZeroIsImmediate(t *testing.T) {
	decision := (retryPolicy{}).next(0, time.Second, 0.75)

	if decision.Exhausted {
		t.Fatal("attempt zero must not be exhausted while budget remains")
	}
	if decision.Delay != 0 {
		t.Fatalf("attempt zero delay = %s, want 0", decision.Delay)
	}
	if decision.EffectiveCap != 0 {
		t.Fatalf("attempt zero effective cap = %s, want 0", decision.EffectiveCap)
	}
}

func TestRetryPolicy_GrowsAndCaps(t *testing.T) {
	policy := retryPolicy{}
	cases := []struct {
		name    string
		attempt int
		wantCap time.Duration
	}{
		{name: "first retry", attempt: 1, wantCap: 200 * time.Millisecond},
		{name: "second retry", attempt: 2, wantCap: 400 * time.Millisecond},
		{name: "third retry", attempt: 3, wantCap: 800 * time.Millisecond},
		{name: "large attempt", attempt: math.MaxInt, wantCap: 5 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := policy.next(tc.attempt, time.Hour, 0)
			if decision.Exhausted {
				t.Fatal("retry with positive budget must not be exhausted")
			}
			if decision.EffectiveCap != tc.wantCap {
				t.Fatalf("effective cap = %s, want %s", decision.EffectiveCap, tc.wantCap)
			}
			if decision.Delay != 0 {
				t.Fatalf("zero sample delay = %s, want 0", decision.Delay)
			}
			if decision.Delay < 0 || decision.Delay >= decision.EffectiveCap {
				t.Fatalf("delay %s outside [0, %s)", decision.Delay, decision.EffectiveCap)
			}
		})
	}
}

func TestRetryPolicy_ClipsToRemainingBudget(t *testing.T) {
	remaining := 125 * time.Millisecond
	decision := (retryPolicy{}).next(4, remaining, 0.5)

	if decision.Exhausted {
		t.Fatal("positive remaining budget must not be exhausted")
	}
	if decision.EffectiveCap != remaining {
		t.Fatalf("effective cap = %s, want remaining budget %s", decision.EffectiveCap, remaining)
	}
	if decision.Delay != 62500*time.Microsecond {
		t.Fatalf("sampled delay = %s, want 62.5ms", decision.Delay)
	}
	if decision.Delay < 0 || decision.Delay >= decision.EffectiveCap || decision.Delay > remaining {
		t.Fatalf("delay %s violates clipped bounds", decision.Delay)
	}
}

func TestRetryPolicy_SampledEndpointsStayWithinCap(t *testing.T) {
	cap := 200 * time.Millisecond
	cases := []struct {
		name   string
		sample float64
		want   time.Duration
	}{
		{name: "zero", sample: 0, want: 0},
		{name: "midpoint", sample: 0.5, want: 100 * time.Millisecond},
		{name: "one", sample: 1, want: cap - time.Nanosecond},
		{name: "positive infinity", sample: math.Inf(1), want: cap - time.Nanosecond},
		{name: "negative", sample: -1, want: 0},
		{name: "negative infinity", sample: math.Inf(-1), want: 0},
		{name: "not a number", sample: math.NaN(), want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := (retryPolicy{}).next(1, time.Second, tc.sample)
			if decision.Delay != tc.want {
				t.Fatalf("sample %v delay = %s, want %s", tc.sample, decision.Delay, tc.want)
			}
			if decision.Delay < 0 || decision.Delay >= decision.EffectiveCap {
				t.Fatalf("delay %s outside [0, %s)", decision.Delay, decision.EffectiveCap)
			}
		})
	}
}

func TestRetryPolicy_StopsWhenBudgetExhausted(t *testing.T) {
	policy := retryPolicy{}
	for _, remaining := range []time.Duration{0, -time.Nanosecond} {
		decision := policy.next(1, remaining, 0.5)
		if !decision.Exhausted {
			t.Fatalf("remaining %s must return an exhausted result", remaining)
		}
		if decision.Delay != 0 || decision.EffectiveCap != 0 {
			t.Fatalf("exhausted result = %+v, want zero delay and cap", decision)
		}
	}
}
