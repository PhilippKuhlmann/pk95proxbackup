// PK95 ProxBackup: backup appliance for Proxmox VE and Proxmox Backup Server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"pk95proxbackup/internal/api"
	"pk95proxbackup/internal/app"
	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/store"
)

// version is set by the release build (-ldflags "-X main.version=…").
var version = "1.0.0"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println(version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "-totp-off" {
		totpOff(os.Args[2:])
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

// totpOff switches two-factor sign-in off for an account (console rescue when the phone and the
// recovery codes are lost): pk95proxbackup -totp-off <user>.
func totpOff(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Aufruf: pk95proxbackup -totp-off <benutzername>")
		os.Exit(2)
	}
	st, err := store.Open(filepath.Join(env("PK95PB_DATA_DIR", "/var/lib/pk95proxbackup"), "pk95proxbackup.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	if err := api.DisableTOTP(st, args[0]); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			log.Fatalf("Konto „%s“ gibt es nicht.", args[0])
		}
		log.Fatal(err)
	}
	fmt.Printf("Zwei-Faktor-Anmeldung für „%s“ ist aus. Anmeldung jetzt nur mit dem Passwort.\n", args[0])
}
