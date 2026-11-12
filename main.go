// PK95 ProxBackup: backup appliance for Proxmox VE and Proxmox Backup Server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"pk95proxbackup/internal/app"
	"pk95proxbackup/internal/core"
)

// version is set by the release build (-ldflags "-X main.version=…").
var version = "1.0.0"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println(version)
		return
	}
	if r := os.Getenv("PK95PB_REPO"); r != "" {
		core.UpdateRepo = r
	}
	cfg := app.Config{
		Version:  version,
		Listen:   env("PK95PB_LISTEN", ":443"),
		HTTPPort: env("PK95PB_HTTP_REDIRECT", ":80"),
		DataDir:  env("PK95PB_DATA_DIR", "/var/lib/pk95proxbackup"),
		Demo:     os.Getenv("PK95PB_DEMO") == "1",
		NoTLS:    os.Getenv("PK95PB_NO_TLS") == "1",
	}
	core.UpdateGuard(cfg.DataDir, version)
	// The path as it is now: after an update /proc/self/exe points to the renamed old file.
	exe, exeErr := os.Executable()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := app.Run(ctx, cfg)
	stop()
	if errors.Is(err, app.ErrRestart) {
		// Start again in place (same process for systemd), with the program now on disk.
		e := exeErr
		if e == nil {
			log.Printf("Neustart …")
			e = syscall.Exec(exe, os.Args, os.Environ())
		}
		log.Fatalf("Neustart fehlgeschlagen: %v", e)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
