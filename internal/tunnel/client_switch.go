package tunnel

import (
	"context"
	"errors"
	"time"
)

func (rt *clientRuntime) switchLoop(ctx context.Context) {
	ticker := time.NewTicker(rt.cfg.Probe.Interval)
	defer ticker.Stop()
	stats := map[string]*probeStat{}
	for {
		select {
		case <-rt.done:
			return
		case <-ticker.C:
		}
		if err := rt.runSwitchCycle(ctx, stats); err != nil && !errors.Is(err, context.Canceled) {
			rt.logger.Printf("event=path_switch status=fail error=%q", err.Error())
		}
	}
}

func (rt *clientRuntime) runSwitchCycle(ctx context.Context, stats map[string]*probeStat) error {
	active, lastSwitch := rt.activeSnapshot()
	if active == nil {
		return nil
	}

	probeResult, cancelProbes := rt.startProbeBatch(ctx, pathsExcept(rt.cfg.Paths, active.path.Name))
	heartbeat := rt.activeHeartbeat(ctx, active, rt.cfg.Probe.Timeout)
	candidates, err := collectSwitchCandidates(probeResult, cancelProbes)
	if err != nil {
		return err
	}
	defer func() {
		if err := closeProbeCandidates(candidates); err != nil {
			rt.logger.Printf("event=probe_candidate_cleanup status=fail error=%q", err.Error())
		}
	}()

	activeStat := pathProbeStat(stats, active.path.Name)
	updateActiveHeartbeatStat(activeStat, heartbeat)
	activeFailed := activeStat.fail >= rt.cfg.Probe.ActiveFailureThreshold
	best := rt.updateCandidateStats(stats, candidates)
	if best == nil {
		if activeFailed {
			rt.dropActive(active)
		}
		return nil
	}
	heartbeatHealthy := heartbeat.Err == nil && !heartbeat.TimedOut && !heartbeat.Closed
	latencyRequest := latencySwitchRequest{
		active: active.path, activeStat: activeStat, candidate: best.Path,
		candidateRTT: best.RTT, lastSwitch: lastSwitch,
	}
	if !activeFailed && (!heartbeatHealthy || !rt.shouldLatencySwitch(latencyRequest)) {
		return nil
	}

	_, err = rt.activateCandidateFor(ctx, candidateActivationRequest{
		candidate: best, resume: true, expected: active, allowDropped: activeFailed,
	})
	if errors.Is(err, errActivationSuperseded) {
		return nil
	}
	return err
}

func (rt *clientRuntime) activeSnapshot() (*clientConn, time.Time) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.active, rt.lastSwitch
}

func collectSwitchCandidates(result <-chan probeBatchOutcome, cancel context.CancelFunc) ([]probeCandidate, error) {
	defer cancel()
	if result == nil {
		return nil, nil
	}
	outcome := <-result
	return outcome.items, outcome.err
}

func pathProbeStat(stats map[string]*probeStat, name string) *probeStat {
	stat := stats[name]
	if stat == nil {
		stat = &probeStat{}
		stats[name] = stat
	}
	return stat
}

func updateActiveHeartbeatStat(stat *probeStat, heartbeat activeHeartbeatResult) {
	if heartbeat.Err != nil || heartbeat.TimedOut || heartbeat.Closed {
		stat.fail++
		stat.success = 0
	} else {
		stat.success++
		stat.fail = 0
		stat.rtt = heartbeat.RTT
	}
}

func (rt *clientRuntime) updateCandidateStats(stats map[string]*probeStat, candidates []probeCandidate) *probeCandidate {
	var best *probeCandidate
	for i := range candidates {
		candidate := &candidates[i]
		stat := pathProbeStat(stats, candidate.Path.Name)
		if candidate.Err != nil {
			stat.fail++
			stat.success = 0
			continue
		}
		stat.success++
		stat.fail = 0
		stat.rtt = candidate.RTT
		if best == nil && stat.success >= rt.cfg.Probe.CandidateSuccessThreshold {
			best = candidate
		}
	}
	return best
}

type latencySwitchRequest struct {
	active       PathConfig
	activeStat   *probeStat
	candidate    PathConfig
	candidateRTT time.Duration
	lastSwitch   time.Time
}

func (rt *clientRuntime) shouldLatencySwitch(request latencySwitchRequest) bool {
	if time.Since(request.lastSwitch) < rt.cfg.Probe.SwitchCooldown {
		return false
	}
	if request.activeStat == nil || request.activeStat.rtt <= 0 {
		return request.candidate.Priority > request.active.Priority
	}
	delta := request.activeStat.rtt - request.candidateRTT
	if delta >= rt.cfg.Probe.BetterRTTMinDelta && float64(delta)/float64(request.activeStat.rtt) >= rt.cfg.Probe.BetterRTTRatio {
		return true
	}
	return absDuration(delta) <= rt.cfg.Probe.BetterRTTMinDelta && request.candidate.Priority > request.active.Priority
}
