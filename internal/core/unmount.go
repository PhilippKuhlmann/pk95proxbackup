package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"pk95proxbackup/internal/prox"
)

// unmountStore unmounts a removable datastore and waits for it. A disk the proxy still holds
// open ("target is busy", seen with a proxy that ran for weeks) is let go by reloading the
// proxy gracefully; a maintenance mode "unmount" left behind by a failed attempt is cleared
// first, because PBS refuses a new attempt while it is set.
func (c *Core) unmountStore(ctx context.Context, pbs *prox.PBS, store string) error {
	try := func() error {
		c.clearUnmountMode(ctx, pbs, store)
		upid, err := pbs.Unmount(ctx, store)
		if err != nil {
			return err
		}
		_, err = pbs.WaitTask(ctx, upid, 10*time.Minute)
		return err
	}
	err := try()
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "busy") {
		return err
	}
	if rerr := pbs.ReloadProxy(ctx); rerr != nil {
		return fmt.Errorf("%w (Neuladen von proxmox-backup-proxy, um die Platte freizugeben, ging nicht: %v)", err, rerr)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
	}
	if err2 := try(); err2 != nil {
		c.clearUnmountMode(ctx, pbs, store)
		return fmt.Errorf("Die Platte wird noch benutzt, auch nach dem Neuladen von proxmox-backup-proxy: %w", err2)
	}
	return nil
}

// clearUnmountMode removes the maintenance mode "unmount" a failed unmount leaves behind.
func (c *Core) clearUnmountMode(ctx context.Context, pbs *prox.PBS, store string) {
	cfgs, err := pbs.Datastores(ctx)
	if err != nil {
		return
	}
	for _, d := range cfgs {
		if d.Name == store && strings.Contains(d.Maintenance, "unmount") {
			_ = pbs.UpdateDatastore(ctx, store, prox.Params{"delete": []string{"maintenance-mode"}})
		}
	}
}
