package core

import "context"

// UnescapeWorkerIDForTest exposes unescapeWorkerID to the external tests.
var UnescapeWorkerIDForTest = unescapeWorkerID

// ReportBackupsForTest exposes reportBackups.
func (c *Core) ReportBackupsForTest(ctx context.Context, ov *Overview) { c.reportBackups(ctx, ov) }
