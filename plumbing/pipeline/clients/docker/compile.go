package docker

import (
	"context"
	"fmt"
	"os"

	"github.com/docker/docker/api/types/mount"
	"github.com/grafana/scribe/docker"
	"github.com/grafana/scribe/plumbing/pipelineutil"
)

// compilePipeline creates a docker container that compiles the provided pipeline so that the compiled pipeline can be mounted in
// other containers without requiring that the container has the scribe command or go installed.
func (c *Client) compilePipeline(ctx context.Context, id string, network *docker.Network) (*docker.Volume, error) {
	log := c.Log

	volume, err := docker.CreateVolume(ctx, c.Client, docker.CreateVolumeOpts{
		Name: fmt.Sprintf("scribe-%s", id),
	})

	if err != nil {
		return nil, fmt.Errorf("error creating docker volume: %w", err)
	}

	cmd := pipelineutil.GoBuild(ctx, pipelineutil.GoBuildOpts{
		Pipeline: c.Opts.Args.Path,
		Module:   "/var/scribe",
		Output:   "/opt/scribe/pipeline",
	})

	mounts, err := DefaultMounts(volume)
	if err != nil {
		return nil, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	mounts = append(mounts, mount.Mount{
		Type:   mount.TypeBind,
		Source: wd,
		Target: "/var/scribe",
		TmpfsOptions: &mount.TmpfsOptions{
			Mode: os.FileMode(0755),
		},
	})

	opts := docker.CreateContainerOpts{
		Name:    fmt.Sprintf("compile-%s", volume.Name),
		Image:   "golang:1.18",
		Command: cmd.Args,
		Mounts:  mounts,
		Workdir: "/var/scribe",
		Env: []string{
			"GOOS=linux",
			"GOARCH=amd64",
			"CGO_ENABLED=0",
		},
		Out: log.Writer(),
	}

	container, err := docker.CreateContainer(ctx, c.Client, opts)
	if err != nil {
		return nil, err
	}

	log.Warnf("Building pipeline binary '%s' in docker volume...", c.Opts.Args.Path)
	// This should run a command very similar to this:
	// docker run --rm -v $TMPDIR:/var/scribe scribe/go:{version} go build -o /var/scribe/pipeline ./{pipeline}
	if err := docker.RunContainer(ctx, c.Client, docker.RunContainerOpts{
		Container: container,
		Stdout:    log.WithField("stream", "stdout").Writer(),
		Stderr:    log.WithField("stream", "stderr").Writer(),
	}); err != nil {
		return nil, err
	}

	return volume, nil
}
