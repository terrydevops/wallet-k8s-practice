package probe

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// lastSuccessLine matches one sample of swap_probe_last_success_timestamp_seconds in the Prometheus
// text format; the scenario label may appear anywhere in the label set.
var lastSuccessLine = regexp.MustCompile(`^swap_probe_last_success_timestamp_seconds\{(?:[^}]*,)?scenario="([^"]*)"[^}]*\}\s+(\S+)`)

// LastSuccess reads the last successful quote time per scenario from a /metrics response.
func LastSuccess(r io.Reader) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "swap_probe_last_success_timestamp_seconds{") {
			continue
		}
		m := lastSuccessLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("unexpected sample: %q", line)
		}
		v, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			return nil, fmt.Errorf("sample value %q: %w", m[2], err)
		}
		sec, frac := math.Modf(v)
		t := time.Unix(int64(sec), int64(frac*1e9))
		if t.After(out[m[1]]) {
			out[m[1]] = t
		}
	}
	return out, sc.Err()
}

// Stale returns the scenarios without a successful quote in the maxAge before now, in scenario order.
func Stale(last map[string]time.Time, scenarios []Scenario, now time.Time, maxAge time.Duration) []string {
	var stale []string
	for _, s := range scenarios {
		t, ok := last[s.Name]
		if !ok || now.Sub(t) > maxAge {
			stale = append(stale, s.Name)
		}
	}
	return stale
}
