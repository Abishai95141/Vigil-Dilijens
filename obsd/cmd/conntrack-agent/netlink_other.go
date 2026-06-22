//go:build !linux

package main

import (
	"errors"
	"io"
)

// On non-Linux platforms there is no conntrack netlink interface. These stubs keep the
// package compiling in CI (macOS) without pulling the Linux-only netlink dependency into
// the build; the agent only ever runs as a Linux DaemonSet, where netlink_linux.go wins.

var errNoNetlink = errors.New("conntrack netlink unavailable on this platform (linux-only)")

func netlinkAvailable() error { return errNoNetlink }

func netlinkConntrack(io.Writer) error { return errNoNetlink }
