package tunnel

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	errActivationSuperseded = errors.New("active connection changed before candidate promotion")
	errClientRuntimeClosed  = errors.New("client runtime is closed")
)

func (rt *clientRuntime) connectAny(ctx context.Context, resume bool) error {
	lkg := rt.lastKnownGoodPath()
	if lkg == nil {
		candidates, err := probeBatch(ctx, rt.cfg.Paths, rt.cfg.Probe.Timeout)
		if err != nil {
			return err
		}
		return rt.activateRankedCandidates(ctx, candidates, resume)
	}

	probePaths := pathsExcept(rt.cfg.Paths, lkg.Name)
	probes, cancelProbes := rt.startProbeBatch(ctx, probePaths)
	if err := rt.activate(ctx, *lkg, resume); err == nil {
		cancelProbes()
		if probes != nil {
			go rt.drainProbeBatch(probes)
		}
		return nil
	} else if probes == nil {
		cancelProbes()
		return err
	}
	candidates := <-probes
	cancelProbes()
	if candidates.err != nil {
		return candidates.err
	}
	return rt.activateRankedCandidates(ctx, candidates.items, resume)
}

type probeBatchOutcome struct {
	items []probeCandidate
	err   error
}

func (rt *clientRuntime) startProbeBatch(ctx context.Context, paths []PathConfig) (<-chan probeBatchOutcome, context.CancelFunc) {
	probeCtx, cancel := context.WithCancel(ctx)
	if len(enabledPaths(paths)) == 0 {
		cancel()
		return nil, cancel
	}
	result := make(chan probeBatchOutcome, 1)
	go func() {
		candidates, err := probeBatch(probeCtx, paths, rt.cfg.Probe.Timeout)
		result <- probeBatchOutcome{items: candidates, err: err}
		close(result)
	}()
	return result, cancel
}

func (rt *clientRuntime) drainProbeBatch(result <-chan probeBatchOutcome) {
	outcome := <-result
	if err := closeProbeCandidates(outcome.items); err != nil {
		rt.logger.Printf("event=background_probe_cleanup status=fail error=%q", err.Error())
	}
}

func (rt *clientRuntime) activateRankedCandidates(ctx context.Context, candidates []probeCandidate, resume bool) error {
	defer func() {
		if err := closeProbeCandidates(candidates); err != nil {
			rt.logger.Printf("event=probe_candidate_cleanup status=fail error=%q", err.Error())
		}
	}()

	failedPaths := make([]PathConfig, 0, len(candidates))
	var last error
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.Err != nil {
			last = candidate.Err
			failedPaths = append(failedPaths, candidate.Path)
			continue
		}
		if _, err := rt.activateCandidate(ctx, candidate, resume); err != nil {
			last = err
			continue
		}
		return nil
	}
	for _, path := range failedPaths {
		if err := rt.activate(ctx, path, resume); err != nil {
			last = err
			continue
		}
		return nil
	}
	if last == nil {
		return fmt.Errorf("no enabled path")
	}
	return last
}

func (rt *clientRuntime) activate(ctx context.Context, path PathConfig, resume bool) error {
	timings, err := rt.activateDirect(ctx, path, resume)
	rt.logDialAttempt(path, timings, err)
	return err
}

func (rt *clientRuntime) activateDirect(ctx context.Context, path PathConfig, resume bool) (DialTimings, error) {
	rt.switchMu.Lock()
	defer rt.switchMu.Unlock()

	sessionID, c2sAck, s2cRx := rt.helloOffsets()
	ws, timings, err := openWebSocket(ctx, path.Endpoint)
	if err != nil {
		return timings, err
	}
	conn, accept, helloTimings, err := startSession(ctx, ws, sessionRequest{
		path: path, name: rt.cfg.Name, resume: resume, sessionID: sessionID,
		clientToServerRx: c2sAck, serverToClientRx: s2cRx,
	})
	timings.MoltSSHHello = helloTimings.MoltSSHHello
	timings.Total += helloTimings.MoltSSHHello
	if err != nil {
		return timings, err
	}
	err = rt.commitActivation(activationCommit{conn: conn, accept: accept, path: path, resume: resume})
	return timings, err
}

func (rt *clientRuntime) activateCandidate(ctx context.Context, candidate *probeCandidate, resume bool) (DialTimings, error) {
	return rt.activateCandidateFor(ctx, candidateActivationRequest{candidate: candidate, resume: resume})
}

type candidateActivationRequest struct {
	candidate    *probeCandidate
	resume       bool
	expected     *clientConn
	allowDropped bool
}

func (rt *clientRuntime) activateCandidateFor(ctx context.Context, request candidateActivationRequest) (DialTimings, error) {
	candidate := request.candidate
	if candidate == nil {
		return DialTimings{}, fmt.Errorf("probe candidate is nil")
	}
	if candidate.Err != nil {
		_ = candidate.close()
		return candidate.DialTimings, candidate.Err
	}

	timings, attempted, err := rt.promoteCandidate(ctx, request)
	if attempted {
		rt.logDialAttempt(candidate.Path, timings, err)
	}
	return timings, err
}

func (rt *clientRuntime) promoteCandidate(ctx context.Context, request candidateActivationRequest) (DialTimings, bool, error) {
	rt.switchMu.Lock()
	defer rt.switchMu.Unlock()

	candidate := request.candidate
	if request.expected != nil && !rt.candidateSourceIsCurrent(request) {
		return candidate.DialTimings, false, errActivationSuperseded
	}
	ws := candidate.transfer()
	if ws == nil {
		return candidate.DialTimings, false, fmt.Errorf("probe candidate has no open connection")
	}
	sessionID, c2sAck, s2cRx := rt.helloOffsets()
	conn, accept, helloTimings, err := startSession(ctx, ws, sessionRequest{
		path:             candidate.Path,
		name:             rt.cfg.Name,
		resume:           request.resume,
		sessionID:        sessionID,
		clientToServerRx: c2sAck,
		serverToClientRx: s2cRx,
	})
	timings := candidate.DialTimings
	timings.MoltSSHHello = helloTimings.MoltSSHHello
	timings.Total += helloTimings.MoltSSHHello
	if err != nil {
		return timings, true, err
	}
	if err := rt.commitActivation(activationCommit{
		conn: conn, accept: accept, path: candidate.Path, resume: request.resume,
	}); err != nil {
		return timings, true, err
	}
	return timings, true, nil
}

func (rt *clientRuntime) candidateSourceIsCurrent(request candidateActivationRequest) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.finished {
		return false
	}
	if rt.active == request.expected {
		return true
	}
	return request.allowDropped && rt.active == nil && rt.epoch == request.expected.epoch
}

type activationCommit struct {
	conn   *clientConn
	accept frameHeader
	path   PathConfig
	resume bool
}

func (rt *clientRuntime) commitActivation(commit activationCommit) error {
	rt.mu.Lock()
	if rt.finished {
		rt.mu.Unlock()
		_ = commit.conn.close()
		return errClientRuntimeClosed
	}
	if commit.resume && commit.accept.SessionID != rt.sessionID {
		rt.mu.Unlock()
		_ = commit.conn.close()
		return fmt.Errorf("server accepted different session")
	}
	if commit.accept.ClientToServerRx > rt.c2sNext {
		rt.mu.Unlock()
		_ = commit.conn.close()
		return fmt.Errorf("server ack is ahead of client")
	}
	if err := rt.advanceC2SAckLocked(commit.accept.ClientToServerRx); err != nil {
		rt.mu.Unlock()
		_ = commit.conn.close()
		return err
	}
	old := rt.active
	rt.active = commit.conn
	rt.sessionID = commit.accept.SessionID
	rt.epoch = commit.accept.Epoch
	rt.lastSwitch = time.Now()
	path := commit.path
	rt.lastKnownGood = &path
	rt.cond.Broadcast()
	rt.mu.Unlock()

	if err := SaveLastKnownGoodPath(rt.cfg, commit.path.Name); err != nil {
		rt.warnPathState("save", err)
	}
	rt.logger.Printf(
		"proxy active path=%s session=%s epoch=%d",
		logToken(commit.path.Name), logToken(commit.accept.SessionID), commit.accept.Epoch,
	)
	if old != nil {
		_ = old.close()
	}
	go rt.receiveLoop(commit.conn)
	return rt.replayClientBytes(commit.conn)
}

func (rt *clientRuntime) helloOffsets() (string, uint64, uint64) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.sessionID, rt.c2sAck, rt.s2cRx
}
