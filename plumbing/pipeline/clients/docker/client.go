package docker

import (
	"context"
	"errors"
	"fmt"

	"github.com/docker/docker/client"
	"github.com/grafana/shipwright/docker"
	"github.com/grafana/shipwright/plumbing"
	"github.com/grafana/shipwright/plumbing/pipeline"
	"github.com/grafana/shipwright/plumbing/plog"
	"github.com/grafana/shipwright/plumbing/stringutil"
	"github.com/grafana/shipwright/plumbing/syncutil"
	"github.com/sirupsen/logrus"
)

// The Client is used when interacting with a shipwright pipeline using the shipwright CLI.
// In order to emulate what happens in a remote environment, the steps are put into a queue before being ran.
// Each step is ran in its own docker container.
type Client struct {
	Client client.APIClient
	Opts   pipeline.CommonOpts

	Log *logrus.Logger
}

func (c *Client) Validate(step pipeline.Step[pipeline.Action]) error {
	if step.Image == "" {
		return errors.New("no image provided")
	}
	return nil
}

func (c *Client) networkName() string {
	uid := stringutil.Random(8)
	return fmt.Sprintf("shipwright-%s-%s", stringutil.Slugify(c.Opts.Name), uid)
}

type walkOpts struct {
	walker  pipeline.Walker
	network *docker.Network
	volume  *docker.Volume
	log     logrus.FieldLogger
}

func (c *Client) Done(ctx context.Context, w pipeline.Walker) error {
	network, err := docker.CreateNetwork(ctx, c.Client, docker.CreateNetworkOpts{
		Name: c.networkName(),
	})

	logger := c.Log.WithFields(plog.PipelineFields(c.Opts))

	logger.Infoln("Compiling pipeline in docker volume...")
	// Every step needs a compiled version of the pipeline in order to know what to do
	// without requiring that every image has a copy of the shipwright binary
	volume, err := c.compilePipeline(ctx, network)
	if err != nil {
		return fmt.Errorf("failed to compile the pipeline in docker. Error: %w", err)
	}

	logger.Infof("Successfully compiled pipeline in volume '%s'", volume.Name)

	logger.Infoln("Running steps in docker")
	return c.Walk(ctx, walkOpts{
		walker:  w,
		network: network,
		volume:  volume,
		log:     logger,
	})
}

func (c *Client) stepWalkFunc(opts walkOpts) pipeline.StepWalkFunc {
	return func(ctx context.Context, steps ...pipeline.Step[pipeline.Action]) error {
		wg := syncutil.NewWaitGroup()

		for _, step := range steps {
			log := opts.log.WithFields(logrus.Fields{
				"step":    step.Name,
				"step_id": step.Serial,
			})

			log.Infoln("Creating container for step. Image:", step.Image)
			container, err := CreateStepContainer(ctx, c.Client, CreateStepContainerOpts{
				Configurer: c,
				Step:       step,
				Network:    opts.network,
				Binary:     "/opt/shipwright/pipeline",
				Pipeline:   c.Opts.Args.Path,
				BuildID:    c.Opts.Args.BuildID,
				Volume:     opts.volume,
				Out:        log.Writer(),
			})
			if err != nil {
				return err
			}
			log.Debugf("Container created for step '%s' with image '%s' and command '%v'", container.CreateOpts.Name, container.CreateOpts.Image, container.CreateOpts.Command)

			wg.Add(func(ctx context.Context) error {
				log = log.WithField("container", container.CreateOpts.Name)
				var (
					stdout = log.WithField("stream", "stdout").Writer()
					stderr = log.WithField("stream", "stderr").Writer()
				)

				opts := docker.RunContainerOpts{
					Container: container,
					Stdout:    stdout,
					Stderr:    stderr,
				}
				log.Debugln("Running container...")
				return docker.RunContainer(ctx, c.Client, opts)
			})
		}

		return wg.Wait(ctx)
	}
}

func (c *Client) runPipeline(ctx context.Context, opts walkOpts, p pipeline.Step[pipeline.Pipeline]) error {
	return opts.walker.WalkSteps(ctx, p.Serial, c.stepWalkFunc(opts))
}

// walkPipelines returns the walkFunc that runs all of the pipelines in docker in the appropriate order.
// TODO: Most of this code looks very similar to the syncutil.StepWaitGroup and the ContainerWaitGroup type in this package.
// There should be a way to reduce it.
func (c *Client) walkPipelines(opts walkOpts) pipeline.PipelineWalkFunc {
	return func(ctx context.Context, pipelines ...pipeline.Step[pipeline.Pipeline]) error {
		var (
			wg = syncutil.NewPipelineWaitGroup()
		)

		// These pipelines run in parallel, but must all complete before continuing on to the next set.
		for i, v := range pipelines {
			log := opts.log.WithField("pipeline", v.Name)
			// If this is a sub-pipeline, then run these steps without waiting on other pipeliens to complete.
			// However, if a sub-pipeline returns an error, then we shoud(?) stop.
			// TODO: This is probably not true... if a sub-pipeline stops then users will probably want to take note of it, but let the rest of the pipeline continue.
			// and see a report of the failure towards the end of the execution.
			if v.Type == pipeline.StepTypeSubPipeline {
				log.Debugln("Found sub-pipeline, running in new goroutine")
				go func(ctx context.Context, p pipeline.Step[pipeline.Pipeline]) {
					if err := c.runPipeline(ctx, opts, p); err != nil {
						c.Log.WithError(err).Errorln("sub-pipeline failed")
					} else {
						log.Debugln("Sub-pipeline completed without error")
					}
				}(ctx, pipelines[i])
				continue
			}
			opts := opts
			opts.log = log
			// Otherwise, add this pipeline to the set that needs to complete before moving on to the next set of pipelines.
			wg.Add(pipelines[i], opts.walker, c.stepWalkFunc(opts))
		}

		if err := wg.Wait(ctx); err != nil {
			return err
		}

		return nil
	}
}

func (c *Client) Walk(ctx context.Context, opts walkOpts) error {
	return opts.walker.WalkPipelines(ctx, c.walkPipelines(opts))
}

// KnownVolumes is a map of default argument to a function used to retrieve the volume the value represents.
// For example, we know that every pipeline is ran alongisde source code.
// The user can supply a "-arg=source={path-to-source}" argument, or we can just
var KnownVolumes = map[pipeline.Argument]func(*plumbing.PipelineArgs) (string, error){
	pipeline.ArgumentSourceFS: func(args *plumbing.PipelineArgs) (string, error) {
		return ".", nil
	},
	pipeline.ArgumentDockerSocketFS: func(*plumbing.PipelineArgs) (string, error) {
		return "/var/run/docker.sock", nil
	},
}

// GetVolumeValue will attempt to find the appropriate volume to mount based on the argument provided.
// Some arguments have known or knowable values, like "ArgumentSourceFS".
func GetVolumeValue(args *plumbing.PipelineArgs, arg pipeline.Argument) (string, error) {
	// If an applicable argument is provided, then we should use that, even if it's a known value.
	if val, err := args.ArgMap.Get(arg.Key); err == nil {
		return val, nil
	}

	// See if we can find a known value for this FS...
	if f, ok := KnownVolumes[arg]; ok {
		return f(args)
	}

	// TODO: Should we request via stdin?
	return "", nil
}

// func (c *Client) runAction(ctx context.Context, pipelinePath string, step pipeline.Step[pipeline.Action]) pipeline.Action {
// 	cmd, err := cmdutil.StepCommand(c, cmdutil.CommandOpts{
// 		Step:    step,
// 		BuildID: c.Opts.Args.BuildID,
// 	})
// 	if err != nil {
// 		c.Log.Fatalln(err)
// 		return nil
// 	}
//
// 	args := []string{}
// 	if len(cmd) > 1 {
// 		args = cmd[1:]
// 	}
//
// 	runOpts := RunOpts{
// 		Image:   step.Image,
// 		Command: PipelineVolumePath,
// 		Volumes: []string{},
// 		Args:    args,
// 	}
//
// 	runOpts = runOpts.WithPipelinePath(pipelinePath)
//
// 	runOpts, err = c.applyArguments(runOpts, step.Arguments)
// 	if err != nil {
// 		c.Log.Fatalln(err)
// 		return nil
// 	}
//
// 	return func(ctx context.Context, opts pipeline.ActionOpts) error {
// 		runOpts.Stdout = opts.Stdout
// 		runOpts.Stderr = opts.Stderr
//
// 		c.Log.Debugf("Running command 'docker %v'", RunArgs(runOpts))
// 		return Run(ctx, runOpts)
// 	}
// }
