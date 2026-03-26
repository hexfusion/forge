package kind

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	// DefaultRegistryName is the container name for the local registry.
	DefaultRegistryName = "forge-registry"

	// DefaultRegistryPort is the host port for the local registry.
	DefaultRegistryPort = "5001"

	// RegistryImage is the registry container image.
	RegistryImage = "registry:2"
)

// RegistryAddress returns the local registry address for image tags.
func RegistryAddress() string {
	port := os.Getenv("FORGE_REGISTRY_PORT")
	if port == "" {
		port = DefaultRegistryPort
	}
	return fmt.Sprintf("localhost:%s", port)
}

// EnsureRegistry starts a local container registry if not already running.
// This follows the Kind local registry pattern:
// https://kind.sigs.k8s.io/docs/user/local-registry/
func EnsureRegistry() error {
	name := DefaultRegistryName
	port := DefaultRegistryPort
	if p := os.Getenv("FORGE_REGISTRY_PORT"); p != "" {
		port = p
	}

	// Check if already running
	out, err := exec.Command("podman", "inspect", "-f", "{{.State.Running}}", name).Output()
	if err == nil && strings.TrimSpace(string(out)) == "true" {
		fmt.Printf("Registry already running at %s\n", RegistryAddress())
		return nil
	}

	// Check if exists but stopped
	if err == nil {
		fmt.Println("Starting existing registry container")
		cmd := exec.Command("podman", "start", name)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Create new registry
	fmt.Printf("Creating local registry at %s\n", RegistryAddress())
	cmd := exec.Command("podman", "run",
		"-d",
		"--restart=always",
		"-p", port+":5000",
		"--name", name,
		RegistryImage,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// StopRegistry stops the local registry.
func StopRegistry() error {
	cmd := exec.Command("podman", "rm", "-f", DefaultRegistryName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RegistryRunning returns true if the local registry is running.
func RegistryRunning() bool {
	out, err := exec.Command("podman", "inspect", "-f", "{{.State.Running}}", DefaultRegistryName).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// KindClusterConfigWithRegistry returns a Kind cluster config that connects
// to the local registry. This enables pods to pull from localhost:5001.
func KindClusterConfigWithRegistry(registryName, registryPort string) string {
	return fmt.Sprintf(`kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors."localhost:%s"]
    endpoint = ["http://%s:%s"]
`, registryPort, registryName, "5000")
}
