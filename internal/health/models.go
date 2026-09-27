package health

import "time"

// applyResult folds one fetch outcome into d. failThreshold is
// Gateway.HealthFailThreshold (N). Returns true when Names contents changed.
//
// Rules (spec §3.2):
//   - fetch error keeps Names (last-known-good) and increments Fails
//   - Fails >= failThreshold forces Expired from any non-expired state
//   - successful non-empty list replaces Names and resets counters
//   - two consecutive empty lists are required to enter KnownEmpty
func (d *ModelDiscovery) applyResult(names []string, fetchErr error, failThreshold int, now time.Time) bool {
	prevNames := d.Names

	if fetchErr != nil {
		d.Fails++
		d.Empties = 0
		d.LastError = fetchErr.Error()
		if failThreshold > 0 && d.Fails >= failThreshold {
			d.State = ModelsExpired
			return false
		}
		switch d.State {
		case ModelsKnown, ModelsDegraded:
			d.State = ModelsDegraded
		case ModelsUntried, ModelsKnownEmpty, ModelsExpired:
			// keep current state
		}
		return false
	}

	// Successful fetch.
	d.Fails = 0
	d.LastError = ""
	d.FetchedAt = now

	if len(names) > 0 {
		d.State = ModelsKnown
		d.Names = append([]string(nil), names...)
		d.Empties = 0
		return !stringSlicesEqual(prevNames, d.Names)
	}

	d.Empties++
	if d.Empties >= 2 {
		d.State = ModelsKnownEmpty
		d.Names = nil
		return len(prevNames) > 0
	}
	return false
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
