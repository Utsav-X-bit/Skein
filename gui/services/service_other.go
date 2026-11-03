//go:build !windows

package services

func managerName() string { return "systemd" }
