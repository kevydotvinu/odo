package logs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/fatih/color"

	odolabels "github.com/redhat-developer/odo/pkg/labels"

	"github.com/redhat-developer/odo/pkg/log"

	"github.com/redhat-developer/odo/pkg/devfile/location"
	odoutil "github.com/redhat-developer/odo/pkg/odo/util"

	"github.com/spf13/cobra"
	ktemplates "k8s.io/kubectl/pkg/util/templates"

	"github.com/redhat-developer/odo/pkg/odo/cmdline"
	"github.com/redhat-developer/odo/pkg/odo/genericclioptions"
	"github.com/redhat-developer/odo/pkg/odo/genericclioptions/clientset"
)

const RecommendedCommandName = "logs"

type LogsOptions struct {
	// context
	Context *genericclioptions.Context
	// clients
	clientset *clientset.Clientset

	// variables
	componentName string
	contextDir    string
	out           io.Writer

	// flags
	devMode    bool
	deployMode bool
	follow     bool
}

var _ genericclioptions.Runnable = (*LogsOptions)(nil)

type logsMode string

const (
	DevMode    logsMode = "dev"
	DeployMode logsMode = "deploy"
)

func NewLogsOptions() *LogsOptions {
	return &LogsOptions{
		out: log.GetStdout(),
	}
}

var logsExample = ktemplates.Examples(`
	# Show logs of all containers
	%[1]s
`)

func (o *LogsOptions) SetClientset(clientset *clientset.Clientset) {
	o.clientset = clientset
}

func (o *LogsOptions) Complete(ctx context.Context, cmdline cmdline.Cmdline, _ []string) error {
	var err error
	o.contextDir, err = os.Getwd()
	if err != nil {
		return err
	}
	isEmptyDir, err := location.DirIsEmpty(o.clientset.FS, o.contextDir)
	if err != nil {
		return err
	}
	if isEmptyDir {
		return errors.New("this command cannot run in an empty directory, run the command in a directory containing source code or initialize using 'odo init'")
	}

	o.Context, err = genericclioptions.New(genericclioptions.NewCreateParameters(cmdline).NeedDevfile(""))
	if err != nil {
		return fmt.Errorf("unable to create context: %v", err)
	}

	o.componentName = o.Context.GetComponentName()

	o.clientset.KubernetesClient.SetNamespace(o.Context.GetProject())

	return nil
}

func (o *LogsOptions) Validate(ctx context.Context) error {
	if o.devMode && o.deployMode {
		return errors.New("pass only one of --dev or --deploy flags; pass no flag to see logs for both modes")
	}
	return nil
}

func (o *LogsOptions) Run(_ context.Context) error {
	var logMode logsMode
	var err error

	if o.devMode {
		logMode = DevMode
	} else if o.deployMode {
		logMode = DeployMode
	}

	var mode string
	switch logMode {
	case DevMode:
		mode = odolabels.ComponentDevMode
	case DeployMode:
		mode = odolabels.ComponentDeployMode
	default:
		mode = odolabels.ComponentAnyMode
	}

	events, err := o.clientset.LogsClient.GetLogsForMode(
		mode,
		o.componentName,
		o.Context.GetProject(),
		o.follow,
	)
	if err != nil {
		return err
	}

	uniqueContainerNames := map[string]struct{}{}
	var goroutines struct{ count int64 } // keep a track of running goroutines so that we don't exit prematurely
	errChan := make(chan error)          // errors are put on this channel
	var mu sync.Mutex

	for {
		select {
		case containerLogs := <-events.Logs:
			uniqueName := getUniqueContainerName(containerLogs.Name, uniqueContainerNames)
			uniqueContainerNames[uniqueName] = struct{}{}
			colour := log.ColorPicker()
			logs := containerLogs.Logs

			if o.follow {
				atomic.AddInt64(&goroutines.count, 1)
				go func(out io.Writer) {
					defer func() {
						atomic.AddInt64(&goroutines.count, -1)
					}()
					err = printLogs(uniqueName, logs, out, colour, &mu)
					if err != nil {
						errChan <- err
					}
					events.Done <- struct{}{}
				}(o.out)
			} else {
				err = printLogs(uniqueName, logs, o.out, colour, &mu)
				if err != nil {
					return err
				}
			}
		case err = <-errChan:
			return err
		case err = <-events.Err:
			return err
		case <-events.Done:
			if goroutines.count == 0 {
				if len(uniqueContainerNames) == 0 {
					// This will be the case when:
					// 1. user specifies --dev flag, but the component's running in Deploy mode
					// 2. user specified --deploy flag, but the component's running in Dev mode
					// 3. user passes no flag, but component is running in neither Dev nor Deploy mode
					fmt.Fprintf(o.out, "no containers running in the specified mode for the component %q\n", o.componentName)
				}
				return nil
			}
		}
	}
}

func getUniqueContainerName(name string, uniqueNames map[string]struct{}) string {
	if _, ok := uniqueNames[name]; ok {
		// name already present in uniqueNames; find another name
		// first check if last character in name is a number; if so increment it, else append name with [1]
		var numStr string
		var last int
		var err error

		split := strings.Split(name, "[")
		if len(split) == 2 {
			numStr = strings.Trim(split[1], "]")
			last, err = strconv.Atoi(numStr)
			if err != nil {
				return ""
			}
			last++
		} else {
			last = 1
		}
		name = fmt.Sprintf("%s[%d]", split[0], last)
		return getUniqueContainerName(name, uniqueNames)
	}
	return name
}

// printLogs prints the logs of the containers with container name prefixed to the log message
func printLogs(containerName string, rd io.ReadCloser, out io.Writer, colour color.Attribute, mu *sync.Mutex) error {
	scanner := bufio.NewScanner(rd)
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		line := scanner.Text()
		err := func() error {
			mu.Lock()
			defer mu.Unlock()
			color.Set(colour)
			defer color.Unset()

			_, err := fmt.Fprintln(out, containerName+": "+line)
			return err
		}()
		if err != nil {
			return err
		}
	}

	return nil
}

func NewCmdLogs(name, fullname string) *cobra.Command {
	o := NewLogsOptions()
	logsCmd := &cobra.Command{
		Use:   name,
		Short: "Show logs of all containers of the component",
		Long: `odo logs shows logs of all containers of the component. 
By default it shows logs of all containers running in both Dev and Deploy mode. It prefixes each log message with the container name.`,
		Example: fmt.Sprintf(logsExample, fullname),
		Args:    cobra.MaximumNArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			genericclioptions.GenericRun(o, cmd, args)
		},
	}
	logsCmd.Flags().BoolVar(&o.devMode, string(DevMode), false, "Show logs for containers running only in Dev mode")
	logsCmd.Flags().BoolVar(&o.deployMode, string(DeployMode), false, "Show logs for containers running only in Deploy mode")
	logsCmd.Flags().BoolVar(&o.follow, "follow", false, "Follow/tail the logs of the pods")

	clientset.Add(logsCmd, clientset.LOGS, clientset.FILESYSTEM)
	logsCmd.Annotations["command"] = "main"
	logsCmd.SetUsageTemplate(odoutil.CmdUsageTemplate)
	return logsCmd
}
