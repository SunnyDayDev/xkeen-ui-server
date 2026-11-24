// xkeen-validator-run is PID 1 in the one-shot validation image. Its deadline
// still applies if the host worker dies after starting the container.
package main

import (
	"context"
	"errors"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/validation"
	"os"
	"os/exec"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(125)
	}
	var args []string
	switch os.Args[1] {
	case "version":
		if len(os.Args) != 2 {
			os.Exit(125)
		}
		args = []string{"version"}
	case "test":
		if len(os.Args) != 3 || !validation.VerifyStagedCatalog("/candidate", os.Args[2]) {
			os.Exit(125)
		}
		args = []string{"run", "-test", "-format", "json", "-confdir", "/candidate"}
	default:
		os.Exit(125)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	os.Exit(run(ctx, "/opt/sbin/xray", args))
}
func run(ctx context.Context, binary string, args []string) int {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return 124
	}
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	return 125
}
