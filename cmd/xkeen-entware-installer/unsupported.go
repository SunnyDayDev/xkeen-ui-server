//go:build !linux || !arm64

package main

import "errors"

func run([]string) error { return errors.New("Entware installation requires Linux ARM64") }
