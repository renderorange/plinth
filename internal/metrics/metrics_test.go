package metrics

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestNodesHealthyMetricRegistered(t *testing.T) {
	m := &dto.Metric{}
	if err := NodesHealthy.Write(m); err != nil {
		t.Fatalf("NodesHealthy not registered or not writable: %v", err)
	}
}

func TestNodesDegradedMetricRegistered(t *testing.T) {
	m := &dto.Metric{}
	if err := NodesDegraded.Write(m); err != nil {
		t.Fatalf("NodesDegraded not registered or not writable: %v", err)
	}
}

func TestNodesDeadMetricRegistered(t *testing.T) {
	m := &dto.Metric{}
	if err := NodesDead.Write(m); err != nil {
		t.Fatalf("NodesDead not registered or not writable: %v", err)
	}
}

func TestRequestsTotalMetricRegistered(t *testing.T) {
	m := &dto.Metric{}
	v := RequestsTotal.WithLabelValues("test", "200")
	v.(interface{ Write(*dto.Metric) error }).Write(m)
}

func TestNodesHealthyCanBeSet(t *testing.T) {
	NodesHealthy.Set(3)
	m := &dto.Metric{}
	NodesHealthy.Write(m)
	if m.GetGauge().GetValue() != 3 {
		t.Errorf("NodesHealthy value = %f, want 3", m.GetGauge().GetValue())
	}
}

func TestNodesDegradedCanBeSet(t *testing.T) {
	NodesDegraded.Set(1)
	m := &dto.Metric{}
	NodesDegraded.Write(m)
	if m.GetGauge().GetValue() != 1 {
		t.Errorf("NodesDegraded value = %f, want 1", m.GetGauge().GetValue())
	}
}

func TestNodesDeadCanBeSet(t *testing.T) {
	NodesDead.Set(2)
	m := &dto.Metric{}
	NodesDead.Write(m)
	if m.GetGauge().GetValue() != 2 {
		t.Errorf("NodesDead value = %f, want 2", m.GetGauge().GetValue())
	}
}

func TestRequestDurationCanBeObserved(t *testing.T) {
	RequestDuration.Observe(0.5)
	m := &dto.Metric{}
	RequestDuration.(interface{ Write(*dto.Metric) error }).Write(m)
	h := m.GetHistogram()
	if h.GetSampleCount() < 1 {
		t.Errorf("RequestDuration sample count = %d, want >= 1", h.GetSampleCount())
	}
}

func TestRequestsTotalCanBeIncremented(t *testing.T) {
	RequestsTotal.WithLabelValues("model1", "200").Inc()
	m := &dto.Metric{}
	RequestsTotal.WithLabelValues("model1", "200").(interface{ Write(*dto.Metric) error }).Write(m)
	if m.GetCounter().GetValue() < 1 {
		t.Errorf("RequestsTotal value = %f, want >= 1", m.GetCounter().GetValue())
	}
}
