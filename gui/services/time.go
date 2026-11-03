package services

import "time"

func timeAfter(ns int) <-chan time.Time { return time.After(time.Duration(ns)) }
