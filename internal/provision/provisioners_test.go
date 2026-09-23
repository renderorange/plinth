package provision

import (
	"testing"
)

func TestProvisionerNames(t *testing.T) {
	provisioners := []struct {
		p    Provisioner
		name string
	}{
		{&DriversProvisioner{}, "drivers"},
		{&PythonProvisioner{}, "python"},
		{&VLLMProvisioner{}, "vllm"},
		{&UserProvisioner{}, "user"},
		{&ModelWeightsProvisioner{}, "models"},
		{&VLLMServiceProvisioner{}, "vllm-service"},
		{&GPUExporterServiceProvisioner{}, "gpu-exporter-service"},
	}

	for _, tt := range provisioners {
		if tt.p.Name() != tt.name {
			t.Errorf("%T.Name() = %q, want %q", tt.p, tt.p.Name(), tt.name)
		}
		if tt.p.Description() == "" {
			t.Errorf("%T.Description() is empty", tt.p)
		}
	}
}
