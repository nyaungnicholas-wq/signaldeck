//go:build !race

package mcp

import "time"

// admissionBudget is the real requirement: the budget path runs on every
// request regardless of the tool, so it must not cost latency. See
// race_on_test.go for why the -race build asserts a looser figure.
const admissionBudget = 1 * time.Microsecond
