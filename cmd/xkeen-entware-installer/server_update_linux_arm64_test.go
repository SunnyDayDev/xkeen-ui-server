package main

import (
	"database/sql"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "server-service", "install-server-stage", "update-server-stage", "uninstall-server":
			afterServerPreflight = waitForMaintenanceClient
			afterServerAdmission = func() error {
				if path := os.Getenv("XKEEN_TEST_SERVER_ADMISSION_CHECKPOINT"); path != "" {
					return maintenanceCheckpoint(path, "candidate-admitted")
				}
				if os.Getenv("XKEEN_TEST_INCOMPATIBLE_SERVER_ADMISSION") == "1" {
					db, err := sql.Open("sqlite", serverDB)
					if err != nil {
						return err
					}
					defer db.Close()
					if _, err := db.Exec("INSERT INTO schema_migrations VALUES ((SELECT max(version)+1 FROM schema_migrations))"); err != nil {
						return err
					}
					return errors.New("synthetic incompatible write after admission")
				}
				if os.Getenv("XKEEN_TEST_INTERRUPT_SERVER_ADMISSION") == "1" {
					return errors.New("synthetic failure after server admission")
				}
				return nil
			}
			if err := run(os.Args[1:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func waitForMaintenanceClient() error {
	path := os.Getenv("XKEEN_TEST_SERVER_MAINTENANCE_CHECKPOINT")
	if path == "" {
		return nil
	}
	return maintenanceCheckpoint(path, "preflight-completed")
}

func maintenanceCheckpoint(path, milestone string) error {
	if err := os.WriteFile(filepath.Join(path, milestone), nil, 0600); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(path, "client-completed")); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("synthetic maintenance checkpoint timed out")
}
