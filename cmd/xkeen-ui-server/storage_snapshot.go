package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
)

func runStorage(args []string, stdout, stderr io.Writer) int {
	const usage = "Usage: xkeen-ui-server storage backup --db-path SOURCE --output FILE | storage restore --backup SOURCE --output FILE"
	if len(args) == 3 && args[0] == "compatibility" && args[1] == "--db-path" {
		format, err := sqlite.MaintenanceCompatibility(context.Background(), args[2])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, format)
		return 0
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, usage)
		return 0
	}
	if len(args) == 0 || (args[0] != "backup" && args[0] != "restore") {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	action := args[0]
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sourceOption := "db-path"
	operation := sqlite.Backup
	if action == "restore" {
		sourceOption = "backup"
		operation = sqlite.Restore
	}
	source := flags.String(sourceOption, "", "protected source database")
	output := flags.String("output", "", "new protected database file")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *source == "" || *output == "" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := operation(ctx, *source, *output); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "storage snapshot verified and saved")
	return 0
}
