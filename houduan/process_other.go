//go:build !windows

package main

import "os/exec"

func configureBackgroundProcess(cmd *exec.Cmd) {}
