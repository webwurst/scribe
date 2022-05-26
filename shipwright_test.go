package shipwright_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/grafana/shipwright"
	"github.com/grafana/shipwright/plumbing"
	"github.com/grafana/shipwright/plumbing/pipeline"
	"github.com/grafana/shipwright/plumbing/pipeline/clients/cli"
	"github.com/grafana/shipwright/plumbing/pipeline/clients/drone"
	"github.com/grafana/shipwright/plumbing/pipeline/dag"
	"github.com/grafana/shipwright/plumbing/plog"
	"github.com/sirupsen/logrus"
)

func logger() *logrus.Logger {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	return logger
}

var testOpts = pipeline.CommonOpts{
	Name:    "test piepline",
	Version: "v0.0.0-test",
	Log:     logger(),
}

func TestNew(t *testing.T) {
	t.Run("New should return a CLIClient when provided the -mode=cli flag", func(t *testing.T) {
		cliArgs := []string{"-mode", "cli"}
		args, err := plumbing.ParseArguments(cliArgs)
		if err != nil {
			t.Fatal(err)
		}

		opts := pipeline.CommonOpts{
			Log:  plog.New(logrus.DebugLevel),
			Args: args,
		}
		sw := shipwright.NewClient(opts, shipwright.NewDefaultCollection(testOpts))

		if reflect.TypeOf(sw.Client) != reflect.TypeOf(&cli.Client{}) {
			t.Fatalf("shipwright.Client is '%v', not a CLIClient", reflect.TypeOf(sw.Client))
		}

		// Because reflect feels iffy to me, also make sure that it does not equal the same type as a different client
		if reflect.TypeOf(sw.Client) == reflect.TypeOf(&drone.Client{}) {
			t.Fatalf("shipwright.Client is '%v', not a CLIClient", reflect.TypeOf(&drone.Client{}))
		}
	})

	t.Run("New should return a DroneClient when provided the -mode=drone flag", func(t *testing.T) {
		cliArgs := []string{"-mode", "drone"}
		args, err := plumbing.ParseArguments(cliArgs)
		if err != nil {
			t.Fatal(err)
		}

		opts := pipeline.CommonOpts{
			Log:  plog.New(logrus.DebugLevel),
			Args: args,
		}

		sw := shipwright.NewClient(opts, shipwright.NewDefaultCollection(testOpts))

		if reflect.TypeOf(sw.Client) != reflect.TypeOf(&drone.Client{}) {
			t.Fatalf("shipwright.Client is '%v', not a DroneClient", reflect.TypeOf(sw.Client))
		}

		// Because reflect feels iffy to me, also make sure that it does not equal the same type as a different client
		if reflect.TypeOf(sw.Client) == reflect.TypeOf(&cli.Client{}) {
			t.Fatalf("shipwright.Client is '%v', not a DroneClient", reflect.TypeOf(&cli.Client{}))
		}
	})
}

func TestShipwrightRun(t *testing.T) {
	t.Run("Using a single Run function", func(t *testing.T) {
		// In this test case we're not providing ensurer data because we are not running 'Done'.
		client := shipwright.NewWithClient(testOpts, newEnsurer())
		client.Run(pipeline.NoOpStep.WithName("step 1"), pipeline.NoOpStep.WithName("step 2"), pipeline.NoOpStep.WithName("step 3"), pipeline.NoOpStep.WithName("step 4"))
		n, err := client.Collection.Graph.Node(shipwright.DefaultPipelineID)
		if err != nil {
			t.Fatal(err)
		}
		dag.EnsureGraphEdges(t, map[int64][]int64{
			0: {5},
			5: {6},
			6: {7},
			7: {8},
		}, n.Value.Graph.Edges)
	})

	t.Run("Using a multiple single-Run functions", func(t *testing.T) {
		// In this test case we're not providing ensurer data because we are not running 'Done'.
		client := shipwright.NewWithClient(testOpts, newEnsurer())
		client.Run(pipeline.NoOpStep.WithName("step 1"))
		client.Run(pipeline.NoOpStep.WithName("step 2"))
		client.Run(pipeline.NoOpStep.WithName("step 3"))
		client.Run(pipeline.NoOpStep.WithName("step 4"))

		n, err := client.Collection.Graph.Node(shipwright.DefaultPipelineID)
		if err != nil {
			t.Fatal(err)
		}

		dag.EnsureGraphEdges(t, map[int64][]int64{
			0: {2},
			2: {4},
			4: {6},
			6: {8},
		}, n.Value.Graph.Edges)
	})

	t.Run("Using a combination of multi and single Run functions", func(t *testing.T) {
		// In this test case we're not providing ensurer data because we are not running 'Done'.
		client := shipwright.NewWithClient(testOpts, newEnsurer())
		client.Run(pipeline.NoOpStep.WithName("step 1"), pipeline.NoOpStep.WithName("step 2"))
		client.Run(pipeline.NoOpStep.WithName("step 3"))
		client.Run(pipeline.NoOpStep.WithName("step 4"), pipeline.NoOpStep.WithName("step 5"))

		n, err := client.Collection.Graph.Node(shipwright.DefaultPipelineID)
		if err != nil {
			t.Fatal(err)
		}

		dag.EnsureGraphEdges(t, map[int64][]int64{
			0: {3},
			3: {4},
			4: {6},
			6: {9},
			9: {10},
		}, n.Value.Graph.Edges)
	})
}

func TestBasicPipeline(t *testing.T) {
	ensurer := newEnsurer([]string{"step 1"}, []string{"step 2", "step 3", "step 4"}, []string{"step 5"})

	client := shipwright.NewWithClient(testOpts, ensurer)

	client.Run(pipeline.NoOpStep.WithName("step 1"))
	client.Parallel(pipeline.NoOpStep.WithName("step 2"), pipeline.NoOpStep.WithName("step 3"), pipeline.NoOpStep.WithName("step 4"))
	client.Run(pipeline.NoOpStep.WithName("step 5"))

	if err := client.Execute(context.Background(), client.Collection); err != nil {
		t.Fatal(err)
	}
}

func TestBasicPipelineWithBackground(t *testing.T) {
	ensurer := newEnsurer([]string{"step 1"}, []string{"step 2"}, []string{"step 7"}, []string{"step 3", "step 4", "step 5"}, []string{"step 6"})

	client := shipwright.NewWithClient(testOpts, ensurer)

	client.Background(pipeline.NoOpStep.WithName("step 1"))
	client.Run(pipeline.NoOpStep.WithName("step 2"))
	client.Parallel(pipeline.NoOpStep.WithName("step 3"), pipeline.NoOpStep.WithName("step 4"), pipeline.NoOpStep.WithName("step 5"))
	client.Run(pipeline.NoOpStep.WithName("step 6"))
	client.Background(pipeline.NoOpStep.WithName("step 7"))

	n, err := client.Collection.Graph.Node(shipwright.DefaultPipelineID)
	if err != nil {
		t.Fatal(err)
	}

	dag.EnsureGraphEdges(t, map[int64][]int64{
		0: {1, 3, 10},
		3: {7},
		7: {9},
	}, n.Value.Graph.Edges)

	if err := client.Execute(context.Background(), client.Collection); err != nil {
		t.Fatal(err)
	}
}
