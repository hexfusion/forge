// Package kind implements the cluster.Provider interface for Kind clusters.
package kind

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/hexfusion/forge/pkg/cluster"
)

func init() {
	cluster.RegisterProvider(&Provider{})
}

// Provider creates and manages Kind clusters.
type Provider struct{}

func (p *Provider) Name() string { return "kind" }

func (p *Provider) Create(opts *cluster.CreateOpts) error {
	args := []string{"create", "cluster", "--name", opts.Name}

	// Use a custom config if provided
	if configPath, ok := opts.Extra["kind_config"]; ok {
		args = append(args, "--config", configPath)
	}

	// Custom image
	if image, ok := opts.Extra["image"]; ok {
		args = append(args, "--image", image)
	}

	fmt.Printf("Creating Kind cluster %q\n", opts.Name)
	cmd := exec.Command("kind", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (p *Provider) Delete(name string) error {
	fmt.Printf("Deleting Kind cluster %q\n", name)
	cmd := exec.Command("kind", "delete", "cluster", "--name", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (p *Provider) Kubeconfig(name string) (string, error) {
	cmd := exec.Command("kind", "get", "kubeconfig", "--name", name)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getting kubeconfig for %q: %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Exists checks if a Kind cluster already exists.
func Exists(name string) bool {
	cmd := exec.Command("kind", "get", "clusters")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// LoadImage loads a container image into the Kind cluster.
func LoadImage(clusterName, image string) error {
	fmt.Printf("Loading image %s into Kind cluster %s\n", image, clusterName)
	cmd := exec.Command("kind", "load", "docker-image", image, "--name", clusterName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
