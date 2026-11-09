// PK95 ProxBackup: backup appliance for Proxmox VE and Proxmox Backup Server.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"pk95proxbackup/internal/app"
)

// version is set by the release build (-ldflags "-X main.version=…").
var version = "1.0.0"

func main() {
	cfg := app.Config{
		Version:  version,
		Listen:   env("PK95PB_LISTEN", ":443"),
		HTTPPort: env("PK95PB_HTTP_REDIRECT", ":80"),
		DataDir:  env("PK95PB_DATA_DIR", "/var/lib/pk95proxbackup"),
		Demo:     os.Getenv("PK95PB_DEMO") == "1",
		NoTLS:    os.Getenv("PK95PB_NO_TLS") == "1",
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
