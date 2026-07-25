package storage

import (
	"fmt"
	"time"
)

// RetentionPolicy uses zero to retain a class indefinitely. Purging is always
// explicit; storage adapters do not start background deletion goroutines.
type RetentionPolicy struct {
	TerminalExecutions time.Duration
	Demonstrations     time.Duration
	DecidedApprovals   time.Duration
	AuditEvents        time.Duration
	DeliveredAudit     time.Duration
	Feedback           time.Duration
	Reviews            time.Duration
	Evaluations        time.Duration
}

func (p RetentionPolicy) Validate() error {
	values := map[string]time.Duration{
		"terminal executions": p.TerminalExecutions,
		"demonstrations":      p.Demonstrations,
		"decided approvals":   p.DecidedApprovals,
		"audit events":        p.AuditEvents,
		"delivered audit":     p.DeliveredAudit,
		"feedback":            p.Feedback,
		"reviews":             p.Reviews,
		"evaluations":         p.Evaluations,
	}
	for name, duration := range values {
		if duration < 0 {
			return fmt.Errorf("%s retention must not be negative", name)
		}
	}
	return nil
}

type RetentionResult struct {
	TerminalExecutions int64
	Demonstrations     int64
	DecidedApprovals   int64
	AuditEvents        int64
	DeliveredAudit     int64
	Feedback           int64
	Reviews            int64
	Evaluations        int64
}
