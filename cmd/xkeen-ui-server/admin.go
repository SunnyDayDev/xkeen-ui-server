package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/storage/sqlite"
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
	db, err := sqlite.Open(ctx, *path)
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
	errInput := errors.New("administrator password requires an interactive terminal or --password-file")
	fd := int(input.Fd())
	if !term.IsTerminal(fd) {
		return nil, errInput
	}
	fmt.Fprint(prompt, "Administrator password: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(prompt)
	if err != nil {
		clear(first)
		return nil, errInput
	}
	fmt.Fprint(prompt, "Confirm administrator password: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(prompt)
	defer clear(second)
	if err != nil {
		clear(first)
		return nil, errInput
	}
	if !bytes.Equal(first, second) {
		clear(first)
		return nil, errors.New("administrator password confirmation differs")
	}
	return first, nil
}
