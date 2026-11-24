package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func runAdmin(args []string, terminal *os.File, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, "Usage: xkeen-ui-server admin setup|reset [--db-path PATH] [--password-file PATH]")
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	reset := args[0] == "reset"
	if !reset && args[0] != "setup" {
		fmt.Fprintln(stderr, "expected admin setup or reset")
		return 2
	}
	flags := flag.NewFlagSet("admin "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("db-path", defaultDBPath, "installation database")
	secret := flags.String("password-file", "", "operator-owned mode-0600 file")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: xkeen-ui-server admin setup|reset [--db-path PATH] [--password-file PATH]")
			return 0
		}
		fmt.Fprintln(stderr, "invalid administrator command arguments")
		return 2
	}
	implicitDB, privateFile := true, false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "db-path":
			implicitDB = false
		case "password-file":
			privateFile = true
		}
	})
	if flags.NArg() != 0 || strings.TrimSpace(*path) == "" || privateFile && strings.TrimSpace(*secret) == "" {
		fmt.Fprintln(stderr, "invalid administrator command arguments")
		return 2
	}
	var password []byte
	var err error
	if privateFile {
		password, err = readAdminPasswordFile(*secret)
	} else {
		password, err = readAdminTerminal(terminal, stderr)
	}
	defer clear(password)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	verifier, err := webadmin.HashPassword(password)
	if err != nil {
		if errors.Is(err, webadmin.ErrInvalidPassword) {
			fmt.Fprintln(stderr, err)
		} else {
			fmt.Fprintln(stderr, "administrator verification unavailable")
		}
		return 1
	}
	clear(password)
	if implicitDB && !reset {
		if err := os.MkdirAll(filepath.Dir(*path), 0700); err != nil {
			fmt.Fprintln(stderr, "administrator database unavailable")
			return 1
		}
	}
	ctx := context.Background()
	open := sqlite.Open
	if reset {
		open = sqlite.OpenExisting
	}
	db, err := open(ctx, *path)
	if err != nil {
		fmt.Fprintln(stderr, "administrator database unavailable")
		return 1
	}
	defer db.Close()
	if err := sqlite.NewWebAdmin(db).SaveVerifier(ctx, verifier, reset); err != nil {
		if errors.Is(err, webadmin.ErrSetupRequired) || errors.Is(err, webadmin.ErrAlreadySetup) {
			fmt.Fprintln(stderr, err)
		} else {
			fmt.Fprintln(stderr, "administrator update unavailable")
		}
		return 1
	}
	fmt.Fprintln(stdout, "administrator password saved")
	return 0
}

func readAdminTerminal(input *os.File, prompt io.Writer) ([]byte, error) {
	fd := int(input.Fd())
	if !term.IsTerminal(fd) {
		return nil, errors.New("administrator password requires an interactive terminal or --password-file")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, errors.New("cannot protect administrator password terminal")
	}
	defer term.Restore(fd, state)
	terminal := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{Reader: adminTerminalInput{ctx, input}, Writer: prompt}, "")
	first, err := terminal.ReadPassword("Administrator password: ")
	if err != nil {
		return nil, errors.New("administrator password input interrupted")
	}
	second, err := terminal.ReadPassword("Confirm administrator password: ")
	if err != nil || ctx.Err() != nil {
		return nil, errors.New("administrator password input interrupted")
	}
	if first != second {
		return nil, errors.New("administrator password confirmation differs")
	}
	return []byte(first), nil
}

// Poll so termination can leave the terminal in its original mode even when
// the operator has not pressed a key.
type adminTerminalInput struct {
	ctx  context.Context
	file *os.File
}

func (in adminTerminalInput) Read(buffer []byte) (int, error) {
	fds := []unix.PollFd{{Fd: int32(in.file.Fd()), Events: unix.POLLIN}}
	for in.ctx.Err() == nil {
		n, err := unix.Poll(fds, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n > 0 {
			return in.file.Read(buffer)
		}
	}
	return 0, in.ctx.Err()
}
