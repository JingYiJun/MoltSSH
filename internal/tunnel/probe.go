package tunnel

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

const (
	maxConcurrentProbes = 8
	DialPhaseProbe      = DialPhase("probe")
)

type probeCandidate struct {
	open        *websocket.Conn
	Path        PathConfig
	RTT         time.Duration
	DialTimings DialTimings
	Err         error
	FailedPhase DialPhase
	order       int
}

func (c *probeCandidate) close() error {
	if c.open == nil {
		return nil
	}
	open := c.open
	c.open = nil
	return open.Close()
}

func (c *probeCandidate) transfer() *websocket.Conn {
	open := c.open
	c.open = nil
	return open
}

type probeOpenFunc func(context.Context, string) (*websocket.Conn, DialTimings, error)

type probeRequest struct {
	Path    PathConfig
	Timeout time.Duration
	Open    probeOpenFunc
	order   int
}

func probePathCandidate(ctx context.Context, request probeRequest) (candidate probeCandidate) {
	candidate.Path = request.Path
	candidate.order = request.order
	started := time.Now()
	probeCtx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()

	open, timings, err := request.Open(probeCtx, request.Path.Endpoint)
	candidate.DialTimings = timings
	if err != nil {
		candidate.Err = err
		var dialErr *DialError
		if errors.As(err, &dialErr) {
			candidate.FailedPhase = dialErr.Phase
		}
		return candidate
	}
	candidate.open = open
	owned := true
	defer func() {
		if owned {
			_ = candidate.close()
		}
	}()

	if deadline, ok := probeCtx.Deadline(); ok {
		if err := open.SetDeadline(deadline); err != nil {
			candidate.Err = phaseError(DialPhaseProbe, err)
			candidate.FailedPhase = DialPhaseProbe
			return candidate
		}
	}
	stopClose := context.AfterFunc(probeCtx, func() { _ = open.Close() })
	nonce := newNonce()
	pingStarted := time.Now()
	if err := writeFrame(open, frameHeader{Type: "ping", Nonce: nonce, SentAtUnixNano: pingStarted.UnixNano()}, nil); err != nil {
		stopClose()
		candidate.Err = phaseError(DialPhaseProbe, probeContextError(probeCtx, err))
		candidate.FailedPhase = DialPhaseProbe
		return candidate
	}
	for {
		frame, _, err := readFrame(open)
		if err != nil {
			stopClose()
			candidate.Err = phaseError(DialPhaseProbe, probeContextError(probeCtx, err))
			candidate.FailedPhase = DialPhaseProbe
			return candidate
		}
		if frame.Type != "pong" || frame.Nonce != nonce {
			continue
		}
		if !stopClose() {
			candidate.Err = phaseError(DialPhaseProbe, contextError(probeCtx, context.Canceled))
			candidate.FailedPhase = DialPhaseProbe
			return candidate
		}
		candidate.RTT = time.Since(pingStarted)
		candidate.DialTimings.ProbeRTT = candidate.RTT
		candidate.DialTimings.Total = time.Since(started)
		if err := open.SetDeadline(time.Time{}); err != nil {
			candidate.Err = phaseError(DialPhaseProbe, err)
			candidate.FailedPhase = DialPhaseProbe
			return candidate
		}
		owned = false
		return candidate
	}
}

func probeContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return context.DeadlineExceeded
	}
	return err
}

func probeBatch(ctx context.Context, paths []PathConfig, timeout time.Duration) ([]probeCandidate, error) {
	results, count := streamProbeCandidates(ctx, paths, timeout)
	if count == 0 {
		return nil, ctx.Err()
	}
	candidates := make([]probeCandidate, 0, count)
	for candidate := range results {
		candidates = append(candidates, candidate)
	}
	rankProbeCandidates(candidates)
	if err := ctx.Err(); err != nil {
		_ = closeProbeCandidates(candidates)
		return candidates, err
	}
	return candidates, nil
}

func streamProbeCandidates(ctx context.Context, paths []PathConfig, timeout time.Duration) (<-chan probeCandidate, int) {
	enabled := enabledPaths(paths)
	type job struct {
		path  PathConfig
		order int
	}
	jobs := make(chan job, len(enabled))
	results := make(chan probeCandidate, len(enabled))
	for order, path := range enabled {
		jobs <- job{path: path, order: order}
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(len(enabled), maxConcurrentProbes) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				results <- probePathCandidate(ctx, probeRequest{
					Path: job.path, Timeout: timeout, Open: openWebSocket, order: job.order,
				})
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	return results, len(enabled)
}

func rankProbeCandidates(candidates []probeCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		leftOK, rightOK := left.Err == nil, right.Err == nil
		if leftOK != rightOK {
			return leftOK
		}
		if leftOK && left.RTT != right.RTT {
			return left.RTT < right.RTT
		}
		if left.Path.Priority != right.Path.Priority {
			return left.Path.Priority > right.Path.Priority
		}
		return left.order < right.order
	})
}

func closeProbeCandidates(candidates []probeCandidate) error {
	errs := make([]error, 0, len(candidates))
	for i := range candidates {
		if err := candidates[i].close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func probePath(ctx context.Context, path PathConfig, timeout time.Duration) (time.Duration, error) {
	candidate := probePathCandidate(ctx, probeRequest{Path: path, Timeout: timeout, Open: openWebSocket})
	defer candidate.close()
	return candidate.RTT, candidate.Err
}
