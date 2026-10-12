package acceptance

import "errors"

// ErrHTTPAttemptBudgetExhausted identifies a local pre-dispatch admission denial.
// It is never inferred from an upstream status or error message.
var ErrHTTPAttemptBudgetExhausted = errors.New("local HTTP attempt budget exhausted")

// HTTPAdmissionDiagnostics separates local denials from physical HTTP attempts.
// ReservedAttempts includes capture setup reservations, not just dispatched HTTP.
// These counters do not certify task or protocol completion.
type HTTPAdmissionDiagnostics struct {
	Version          int  `json:"version"`
	MaxAttempts      int  `json:"max_attempts"`
	ReservedAttempts int  `json:"reserved_attempts"`
	BudgetRejections int  `json:"budget_rejections"`
	ClosedRejections int  `json:"closed_rejections"`
	FailedRejections int  `json:"failed_rejections"`
	ActiveBodies     int  `json:"active_bodies"`
	JournalClosed    bool `json:"journal_closed"`
	JournalFailed    bool `json:"journal_failed"`
}

// AdmissionDiagnostics returns a synchronized snapshot without request content.
func (j *HTTPAttemptJournal) AdmissionDiagnostics() HTTPAdmissionDiagnostics {
	j.mu.Lock()
	defer j.mu.Unlock()
	return HTTPAdmissionDiagnostics{Version: 1, MaxAttempts: j.maxAttempts, ReservedAttempts: j.attempts,
		BudgetRejections: j.budgetRejections, ClosedRejections: j.closedRejections, FailedRejections: j.failedRejections,
		ActiveBodies: j.active, JournalClosed: j.closed, JournalFailed: j.err != nil}
}
