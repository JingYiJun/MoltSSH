package tunnel

import (
	"math"
	"time"
)

const (
	retryBaseDelay = 200 * time.Millisecond
	retryMaxDelay  = 5 * time.Second
)

type retryPolicy struct{}

type retryDecision struct {
	Delay        time.Duration
	EffectiveCap time.Duration
	Exhausted    bool
}

func (retryPolicy) next(attempt int, remaining time.Duration, sample float64) retryDecision {
	if remaining <= 0 {
		return retryDecision{Exhausted: true}
	}
	if attempt <= 0 {
		return retryDecision{}
	}

	effectiveCap := retryCap(attempt)
	if remaining < effectiveCap {
		effectiveCap = remaining
	}
	return retryDecision{
		Delay:        retryJitter(effectiveCap, sample),
		EffectiveCap: effectiveCap,
	}
}

func retryCap(attempt int) time.Duration {
	cap := retryBaseDelay
	for step := 1; step < attempt && cap < retryMaxDelay; step++ {
		if cap > retryMaxDelay/2 {
			return retryMaxDelay
		}
		cap *= 2
	}
	return cap
}

func retryJitter(cap time.Duration, sample float64) time.Duration {
	if cap <= 0 {
		return 0
	}
	if math.IsNaN(sample) || sample <= 0 {
		return 0
	}
	if math.IsInf(sample, 1) || sample >= 1 {
		sample = math.Nextafter(1, 0)
	}

	delay := time.Duration(float64(cap) * sample)
	if delay < 0 {
		return 0
	}
	if delay >= cap {
		return cap - time.Nanosecond
	}
	return delay
}
