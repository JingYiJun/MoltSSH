package tunnel

import (
	"context"
	"fmt"
)

func (rt *clientRuntime) activateFirstAvailable(ctx context.Context, resume bool) error {
	probeCtx, cancel := context.WithCancel(ctx)
	results, count := streamProbeCandidates(probeCtx, rt.cfg.Paths, rt.cfg.Probe.Timeout)
	if count == 0 {
		cancel()
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("no enabled path")
	}

	failed := make([]probeCandidate, 0, count)
	var last error
	for candidate := range results {
		if candidate.Err != nil {
			last = candidate.Err
			failed = append(failed, candidate)
			continue
		}
		if _, err := rt.activateCandidate(ctx, &candidate, resume); err != nil {
			last = err
			continue
		}
		go rt.drainProbeCandidates(results, cancel)
		return nil
	}
	cancel()
	if len(failed) > 0 {
		rankProbeCandidates(failed)
		return rt.activateRankedCandidates(ctx, failed, resume)
	}
	if last == nil {
		return fmt.Errorf("no enabled path")
	}
	return last
}

func (rt *clientRuntime) drainProbeCandidates(results <-chan probeCandidate, cancel context.CancelFunc) {
	defer cancel()
	for candidate := range results {
		if err := candidate.close(); err != nil {
			rt.logger.Printf("event=background_probe_cleanup status=fail error=%q", err.Error())
		}
	}
}
