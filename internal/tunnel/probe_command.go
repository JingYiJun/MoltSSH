package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"slices"
	"sort"
	"strings"
)

var errProbePathFailed = errors.New("one or more probe paths failed")

func Probe(ctx context.Context, cfg *Config, stdout io.Writer) (err error) {
	candidates, batchErr := probeBatch(ctx, cfg.Paths, cfg.Probe.Timeout)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].order < candidates[j].order
	})
	defer func() {
		err = errors.Join(err, closeProbeCandidates(candidates))
	}()

	var probeErr error
	writeErrors := make([]error, 0)
	for _, candidate := range candidates {
		if candidate.Err != nil {
			probeErr = errProbePathFailed
		}
		record := formatProbeRecord(candidate)
		log.Printf("probe %s", record)
		if _, writeErr := fmt.Fprintln(stdout, record); writeErr != nil {
			writeErrors = append(writeErrors, fmt.Errorf("write probe result for path %q: %w", candidate.Path.Name, writeErr))
		}
	}
	return errors.Join(batchErr, probeErr, errors.Join(writeErrors...))
}

func formatProbeRecord(candidate probeCandidate) string {
	status := "ok"
	failedPhase := ""
	errorMessage := ""
	endpoint := redactEndpoint(candidate.Path.Endpoint)
	if candidate.Err != nil {
		status = "fail"
		failedPhase = string(candidate.FailedPhase)
		errorMessage = strings.ReplaceAll(candidate.Err.Error(), candidate.Path.Endpoint, endpoint)
		originalURL, originalErr := url.Parse(candidate.Path.Endpoint)
		redactedURL, redactedErr := url.Parse(endpoint)
		if originalErr == nil && redactedErr == nil {
			originalQuery := originalURL.Query()
			redactedQuery := redactedURL.Query()
			for key, values := range originalQuery {
				if slices.Equal(values, redactedQuery[key]) {
					continue
				}
				for _, value := range values {
					if value == "" {
						continue
					}
					errorMessage = strings.ReplaceAll(errorMessage, value, "redacted")
					errorMessage = strings.ReplaceAll(errorMessage, url.QueryEscape(value), "redacted")
				}
			}
		}
		errorMessage = strings.NewReplacer("\r", `\r`, "\n", `\n`).Replace(errorMessage)
	}
	return fmt.Sprintf(
		"path=%s status=%s dns=%s tcp=%s tls=%s websocket_upgrade=%s probe_rtt=%s total=%s failed_phase=%s endpoint=%s error=%s",
		candidate.Path.Name,
		status,
		candidate.DialTimings.DNS,
		candidate.DialTimings.TCP,
		candidate.DialTimings.TLS,
		candidate.DialTimings.WebSocketUpgrade,
		candidate.DialTimings.ProbeRTT,
		candidate.DialTimings.Total,
		failedPhase,
		endpoint,
		errorMessage,
	)
}
