package tunnel

import (
	"errors"
	"net/url"
	"strings"
)

func (rt *clientRuntime) logDialAttempt(path PathConfig, timings DialTimings, attemptErr error) {
	status := "ok"
	failedPhase := ""
	errorMessage := ""
	if attemptErr != nil {
		status = "fail"
		var dialErr *DialError
		if errors.As(attemptErr, &dialErr) {
			failedPhase = string(dialErr.Phase)
		}
		errorMessage = rt.redactDialError(path.Endpoint, attemptErr)
	}
	rt.logger.Printf(
		"event=proxy_dial path=%s status=%s failed_phase=%s dns=%s tcp=%s tls=%s websocket_upgrade=%s moltssh_hello=%s probe_rtt=%s total=%s error=%q",
		logToken(path.Name), status, failedPhase, timings.DNS, timings.TCP, timings.TLS,
		timings.WebSocketUpgrade, timings.MoltSSHHello, timings.ProbeRTT, timings.Total, errorMessage,
	)
}

func (rt *clientRuntime) redactDialError(endpoint string, attemptErr error) string {
	message := strings.ReplaceAll(attemptErr.Error(), endpoint, "redacted")
	if parsed, err := url.Parse(endpoint); err == nil {
		for _, values := range parsed.Query() {
			for _, value := range values {
				if value == "" {
					continue
				}
				message = strings.ReplaceAll(message, value, "redacted")
				message = strings.ReplaceAll(message, url.QueryEscape(value), "redacted")
			}
		}
	}
	rt.mu.Lock()
	sessionID := rt.sessionID
	rt.mu.Unlock()
	if sessionID != "" {
		message = strings.ReplaceAll(message, sessionID, "redacted")
	}
	return message
}

func (rt *clientRuntime) warnPathState(operation string, err error) {
	rt.logger.Printf("event=path_state_%s status=fail error=%q", operation, err.Error())
}

func logToken(value string) string {
	return strings.NewReplacer(" ", "_", "\t", "_", "\r", "_", "\n", "_").Replace(value)
}
