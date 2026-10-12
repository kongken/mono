// Package health probes dependencies without returning credentials or raw driver errors.
package health

import (
	"context"
	"sync"
	"time"
)

type Status struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
}
type Probe struct {
	Name  string
	Check func(context.Context) error
}
type Checker struct{ probes []Probe }

func New(probes ...Probe) *Checker { return &Checker{probes: probes} }
func (c *Checker) Check(ctx context.Context) []Status {
	result := make([]Status, len(c.probes))
	var wg sync.WaitGroup
	for i, probe := range c.probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			result[i] = Status{Name: probe.Name, Ready: probe.Check(bounded) == nil}
		}()
	}
	wg.Wait()
	return result
}
func Ready(statuses []Status) bool {
	if len(statuses) == 0 {
		return false
	}
	for _, s := range statuses {
		if !s.Ready {
			return false
		}
	}
	return true
}
