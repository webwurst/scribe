package shipwright_test

import (
	"reflect"
	"testing"

	shipwright "pkg.grafana.com/shipwright/v1"
	"pkg.grafana.com/shipwright/v1/plumbing"
)

func TestNew(t *testing.T) {
	t.Run("New should return a CLIClient when provided the -mode=cli flag", func(t *testing.T) {
		cliArgs := []string{"-mode", "cli"}
		args, err := plumbing.ParseArguments(cliArgs)
		if err != nil {
			t.Fatal(err)
		}

		sw := shipwright.NewFromOpts(&shipwright.CommonOpts{
			Args: args,
		})

		if reflect.TypeOf(sw.Client) != reflect.TypeOf(&shipwright.CLIClient{}) {
			t.Fatalf("shipwright.Client is '%v',  not a CLIClient", reflect.TypeOf(sw.Client))
		}

		// Because reflect feels iffy to me, also make sure that it does not equal the same type as a different client
		if reflect.TypeOf(sw.Client) == reflect.TypeOf(&shipwright.DroneClient{}) {
			t.Fatalf("shipwright.Client is '%v', not a CLIClient", reflect.TypeOf(&shipwright.DroneClient{}))
		}
	})

	t.Run("New should return a DroneClient when provided the -mode=config flag", func(t *testing.T) {
		cliArgs := []string{"-mode", "drone"}
		args, err := plumbing.ParseArguments(cliArgs)
		if err != nil {
			t.Fatal(err)
		}

		sw := shipwright.NewFromOpts(&shipwright.CommonOpts{
			Args: args,
		})

		if reflect.TypeOf(sw.Client) != reflect.TypeOf(&shipwright.DroneClient{}) {
			t.Fatalf("shipwright.Client is '%v',  not a DroneClient", reflect.TypeOf(sw.Client))
		}

		// Because reflect feels iffy to me, also make sure that it does not equal the same type as a different client
		if reflect.TypeOf(sw.Client) == reflect.TypeOf(&shipwright.CLIClient{}) {
			t.Fatalf("shipwright.Client is '%v', not a DroneClient", reflect.TypeOf(&shipwright.CLIClient{}))
		}
	})
}
