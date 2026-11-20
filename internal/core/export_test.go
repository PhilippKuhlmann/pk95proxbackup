package core

import (
	"context"
	"time"
)

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

// CheckPlanForTest exposes checkPlan.
func (c *Core) CheckPlanForTest(r Rotation, st RotationState, now time.Time) { c.checkPlan(r, st, now) }

// CheckUnpluggedForTest exposes checkUnplugged.
func (c *Core) CheckUnpluggedForTest(ctx context.Context, r Rotation, st RotationState, now time.Time) {
	c.checkUnplugged(ctx, r, st, now)
}

// ResumeForTest exposes resumeRotations.
func (c *Core) ResumeForTest(ctx context.Context) { c.resumeRotations(ctx) }

// ScanForeignForTest exposes scanForeignDisks.
func (c *Core) ScanForeignForTest(ctx context.Context, now time.Time) { c.scanForeignDisks(ctx, now) }

func (c *Core) CheckFailedForTest(ctx context.Context, ov *Overview) { c.checkFailedTasks(ctx, ov) }

// SetUpgradingForTest pretends a system update of a Backup Server runs (BusyWork).
func (c *Core) SetUpgradingForTest(id string, on bool) {
	if on {
		c.upgrades.following.Store(id, true)
	} else {
		c.upgrades.following.Delete(id)
	}
}

// ScheduleHostBackupsForTest exposes scheduleHostBackups.
func (c *Core) ScheduleHostBackupsForTest(now time.Time) { c.scheduleHostBackups(now) }

// HostBackupScriptForTest exposes hostBackupScript.
var HostBackupScriptForTest = hostBackupScript

// HostCheckScriptForTest exposes hostCheckScript.
var HostCheckScriptForTest = hostCheckScript

// ScheduleHandbookForTest exposes scheduleHandbook.
func (c *Core) ScheduleHandbookForTest(now time.Time) { c.scheduleHandbook(now) }

// FreeDiskForTest exposes freeDisk.
var FreeDiskForTest = freeDisk
