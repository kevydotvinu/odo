package binding

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	ktemplates "k8s.io/kubectl/pkg/util/templates"

	"github.com/redhat-developer/odo/pkg/binding/backend"
	"github.com/redhat-developer/odo/pkg/log"
	"github.com/redhat-developer/odo/pkg/odo/cmdline"
	"github.com/redhat-developer/odo/pkg/odo/genericclioptions"
	"github.com/redhat-developer/odo/pkg/odo/genericclioptions/clientset"
)

// BindingRecommendedCommandName is the recommended binding sub-command name
const BindingRecommendedCommandName = "binding"

var removeBindingExample = ktemplates.Examples(`
# Remove binding between service named 'myservice' and the component present in the working directory
%[1]s --name myservice 

`)

type RemoveBindingOptions struct {
	// Flags passed to the command
	flags map[string]string

	// Context
	*genericclioptions.Context

	// Clients
	clientset *clientset.Clientset
}

var _ genericclioptions.Runnable = (*RemoveBindingOptions)(nil)

// NewRemoveBindingOptions returns new instance of ComponentOptions
func NewRemoveBindingOptions() *RemoveBindingOptions {
	return &RemoveBindingOptions{}
}

func (o *RemoveBindingOptions) SetClientset(clientset *clientset.Clientset) {
	o.clientset = clientset
}

func (o *RemoveBindingOptions) Complete(ctx context.Context, cmdline cmdline.Cmdline, args []string) (err error) {
	o.Context, err = genericclioptions.New(genericclioptions.NewCreateParameters(cmdline).NeedDevfile(""))
	if err != nil {
		return err
	}

	// this ensures that the current namespace is used
	o.clientset.KubernetesClient.SetNamespace(o.GetProject())

	o.flags = o.clientset.BindingClient.GetFlags(cmdline.GetFlags())

	return nil
}

func (o *RemoveBindingOptions) Validate(ctx context.Context) (err error) {
	return o.clientset.BindingClient.ValidateRemoveBinding(o.flags)
}

func (o *RemoveBindingOptions) Run(_ context.Context) error {

	devfileobj, err := o.clientset.BindingClient.RemoveBinding(o.flags[backend.FLAG_NAME], o.EnvSpecificInfo.GetDevfileObj())
	if err != nil {
		return err
	}

	err = devfileobj.WriteYamlDevfile()
	if err != nil {
		return err
	}
	log.Success("Successfully removed the binding from the devfile. You can now run `odo dev` or `odo deploy` to delete it from the cluster.")
	return nil
}

// NewCmdBinding implements the component odo sub-command
func NewCmdBinding(name, fullName string) *cobra.Command {
	o := NewRemoveBindingOptions()

	var bindingCmd = &cobra.Command{
		Use:     name,
		Short:   "Remove Binding",
		Long:    "Remove a binding between a service and the component from the devfile",
		Args:    genericclioptions.NoArgsAndSilenceJSON,
		Example: fmt.Sprintf(removeBindingExample, fullName),
		Run: func(cmd *cobra.Command, args []string) {
			genericclioptions.GenericRun(o, cmd, args)
		},
	}
	bindingCmd.Flags().String(backend.FLAG_NAME, "", "Name of the Binding to create")
	clientset.Add(bindingCmd, clientset.BINDING)

	return bindingCmd
}
