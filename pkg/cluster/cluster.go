// Package cluster manages OpenShift and Kubernetes cluster creation.
// Migrated from github.com/hexfusion/dev-installer/pkg/cluster.
package cluster

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewCommand returns the `forge cluster` command tree.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Create and manage clusters",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
		},
	}

	cmd.AddCommand(newCreateCommand())
	return cmd
}

func newCreateCommand() *cobra.Command {
	opts := &createOpts{}

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run()
		},
	}

	opts.addFlags(cmd)
	return cmd
}

type createOpts struct {
	provider       string
	name           string
	releaseImage   string
	releaseType    string
	sshKeyPath     string
	replicasMaster int
	replicasWorker int
	baseDir        string
	providerRegion string
}

func (o *createOpts) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&o.provider, "provider", "p", "", "cluster provider (aws, gcp, azure, libvirt)")
	cmd.Flags().StringVarP(&o.name, "name", "n", "", "cluster name")
	cmd.Flags().StringVarP(&o.releaseImage, "release", "r", "", "release image")
	cmd.Flags().StringVarP(&o.releaseType, "release-type", "t", "", "release image type (ci, nightly, custom)")
	cmd.Flags().StringVarP(&o.sshKeyPath, "ssh-path", "s", "", "path to public ssh key")
	cmd.Flags().IntVarP(&o.replicasMaster, "replicas-master", "m", 3, "master replicas")
	cmd.Flags().IntVarP(&o.replicasWorker, "replicas-worker", "w", 3, "worker replicas")
	cmd.Flags().StringVar(&o.baseDir, "base-dir", defaultBaseDir(), "base directory for cluster data")
	cmd.Flags().StringVar(&o.providerRegion, "provider-region", "", "provider region override")

	cmd.MarkFlagRequired("provider")
	cmd.MarkFlagRequired("release")
	cmd.MarkFlagRequired("ssh-path")
}

func (o *createOpts) validate() error {
	if o.provider == "" {
		return fmt.Errorf("--provider is required")
	}
	if o.releaseImage == "" {
		return fmt.Errorf("--release is required")
	}
	if o.sshKeyPath == "" {
		return fmt.Errorf("--ssh-path is required")
	}
	return nil
}

func (o *createOpts) run() error {
	// TODO: migrate full cluster creation logic from dev-installer
	fmt.Printf("forge cluster create: provider=%s release=%s (not yet implemented)\n", o.provider, o.releaseImage)
	return nil
}

func defaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/forge/clusters"
	}
	return home + "/clusters"
}
