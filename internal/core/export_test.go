package core

import "context"

// UnescapeWorkerIDForTest exposes unescapeWorkerID to the external tests.
var UnescapeWorkerIDForTest = unescapeWorkerID

// ReportBackupsForTest exposes reportBackups.
func (c *Core) ReportBackupsForTest(ctx context.Context, ov *Overview) { c.reportBackups(ctx, ov) }

// CopyRunForTest exposes copyRun.
var CopyRunForTest = copyRun

// SetExecForTest replaces the program start of the update guard.
func SetExecForTest(f func(string, []string, []string) error) func() {
	old := execProgram
	execProgram = f
	return func() { execProgram = old }
}
