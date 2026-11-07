package tray

import (
	"fmt"
	"strings"

	"github.com/samhudson/signals/core/engine"
)

// Summary is "1 needs you · 2 working · 1 done", or "" with no sessions.
func Summary(s engine.State) string {
	var parts []string
	for _, p := range [][2]string{{"waiting", "needs you"}, {"working", "working"}, {"error", "stopped"}, {"done", "done"}} {
		if n := s.Counts[p[0]]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, p[1]))
		}
	}
	return strings.Join(parts, " · ")
}
