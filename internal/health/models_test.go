package health

import (
	"errors"
	"testing"
	"time"
)

func TestApplyResult(t *testing.T) {
	now := time.Now()
	errBoom := errors.New("boom")

	tests := []struct {
		name          string
		start         ModelDiscovery
		names         []string
		fetchErr      error
		threshold     int
		wantState     ModelState
		wantNames     []string
		wantFails     int
		wantEmpties   int
		wantChanged   bool
		wantFetchedAt bool
	}{
		{
			name:          "untried success non-empty",
			start:         ModelDiscovery{State: ModelsUntried},
			names:         []string{"a"},
			threshold:     3,
			wantState:     ModelsKnown,
			wantNames:     []string{"a"},
			wantChanged:   true,
			wantFetchedAt: true,
		},
		{
			name:        "untried fail stays untried",
			start:       ModelDiscovery{State: ModelsUntried},
			fetchErr:    errBoom,
			threshold:   3,
			wantState:   ModelsUntried,
			wantFails:   1,
			wantChanged: false,
		},
		{
			name:        "untried fails reach N expires",
			start:       ModelDiscovery{State: ModelsUntried, Fails: 2},
			fetchErr:    errBoom,
			threshold:   3,
			wantState:   ModelsExpired,
			wantFails:   3,
			wantChanged: false,
		},
		{
			name:          "known fail keeps list and degrades",
			start:         ModelDiscovery{State: ModelsKnown, Names: []string{"a"}, FetchedAt: now},
			fetchErr:      errBoom,
			threshold:     3,
			wantState:     ModelsDegraded,
			wantNames:     []string{"a"},
			wantFails:     1,
			wantChanged:   false,
			wantFetchedAt: true,
		},
		{
			name:        "degraded fail at N expires keeps list",
			start:       ModelDiscovery{State: ModelsDegraded, Names: []string{"a"}, Fails: 2},
			fetchErr:    errBoom,
			threshold:   3,
			wantState:   ModelsExpired,
			wantNames:   []string{"a"},
			wantFails:   3,
			wantChanged: false,
		},
		{
			name:          "degraded success recovers",
			start:         ModelDiscovery{State: ModelsDegraded, Names: []string{"a"}, Fails: 2},
			names:         []string{"a", "b"},
			threshold:     3,
			wantState:     ModelsKnown,
			wantNames:     []string{"a", "b"},
			wantChanged:   true,
			wantFetchedAt: true,
		},
		{
			name:          "first empty keeps prior state",
			start:         ModelDiscovery{State: ModelsKnown, Names: []string{"a"}, FetchedAt: now},
			names:         []string{},
			threshold:     3,
			wantState:     ModelsKnown,
			wantNames:     []string{"a"},
			wantEmpties:   1,
			wantChanged:   false,
			wantFetchedAt: true,
		},
		{
			name:          "second empty enters known_empty and clears",
			start:         ModelDiscovery{State: ModelsKnown, Names: []string{"a"}, Empties: 1, FetchedAt: now},
			names:         []string{},
			threshold:     3,
			wantState:     ModelsKnownEmpty,
			wantNames:     nil,
			wantEmpties:   2,
			wantChanged:   true,
			wantFetchedAt: true,
		},
		{
			name:          "empty while untried stays untried",
			start:         ModelDiscovery{State: ModelsUntried},
			names:         []string{},
			threshold:     3,
			wantState:     ModelsUntried,
			wantEmpties:   1,
			wantChanged:   false,
			wantFetchedAt: true,
		},
		{
			name:        "known_empty fail stays known_empty",
			start:       ModelDiscovery{State: ModelsKnownEmpty, Empties: 2},
			fetchErr:    errBoom,
			threshold:   3,
			wantState:   ModelsKnownEmpty,
			wantFails:   1,
			wantEmpties: 0,
			wantChanged: false,
		},
		{
			name:          "success resets counters",
			start:         ModelDiscovery{State: ModelsDegraded, Names: []string{"a"}, Fails: 2, Empties: 1, LastError: "x"},
			names:         []string{"a"},
			threshold:     3,
			wantState:     ModelsKnown,
			wantNames:     []string{"a"},
			wantChanged:   false,
			wantFetchedAt: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.start
			prevFetched := d.FetchedAt
			changed := d.applyResult(tt.names, tt.fetchErr, tt.threshold, now)

			if d.State != tt.wantState {
				t.Errorf("State = %v, want %v", d.State, tt.wantState)
			}
			if len(d.Names) != len(tt.wantNames) {
				t.Fatalf("Names = %v, want %v", d.Names, tt.wantNames)
			}
			for i := range tt.wantNames {
				if d.Names[i] != tt.wantNames[i] {
					t.Errorf("Names = %v, want %v", d.Names, tt.wantNames)
				}
			}
			if d.Fails != tt.wantFails {
				t.Errorf("Fails = %d, want %d", d.Fails, tt.wantFails)
			}
			if d.Empties != tt.wantEmpties {
				t.Errorf("Empties = %d, want %d", d.Empties, tt.wantEmpties)
			}
			if changed != tt.wantChanged {
				t.Errorf("listChanged = %v, want %v", changed, tt.wantChanged)
			}
			if tt.wantFetchedAt {
				if d.FetchedAt.IsZero() {
					t.Error("FetchedAt still zero, want set")
				}
			} else if d.FetchedAt != prevFetched {
				t.Errorf("FetchedAt changed on failure: %v -> %v", prevFetched, d.FetchedAt)
			}
			if tt.fetchErr != nil {
				if d.LastError == "" {
					t.Error("LastError empty after fetch error")
				}
			} else if d.LastError != "" {
				t.Errorf("LastError = %q after success, want empty", d.LastError)
			}
		})
	}
}
