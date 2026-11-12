//go:build !windows && !android

package services

func managerName() string { return "systemd" }
