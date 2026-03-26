package cluster

// Provider defines the interface for cluster creation backends.
// Each provider (k3s, kind, openshift) implements this to handle
// provider-specific cluster lifecycle.
type Provider interface {
	// Name returns the provider identifier (e.g., "k3s", "kind", "openshift").
	Name() string

	// Create provisions a new cluster with the given options.
	Create(opts *CreateOpts) error

	// Delete tears down the cluster.
	Delete(name string) error

	// Kubeconfig returns the path to the cluster's kubeconfig.
	Kubeconfig(name string) (string, error)
}

// CreateOpts are provider-agnostic cluster creation options.
type CreateOpts struct {
	Name           string
	MasterReplicas int
	WorkerReplicas int
	SSHKeyPath     string
	BaseDir        string

	// Provider-specific options passed through as key-value pairs.
	Extra map[string]string
}

// Registry holds available providers.
var Registry = map[string]Provider{}

// RegisterProvider adds a provider to the registry.
func RegisterProvider(p Provider) {
	Registry[p.Name()] = p
}
