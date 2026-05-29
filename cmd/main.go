/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"os"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/opendatahub-io/ai-gateway-operator/cmd/chartgen"
	"github.com/opendatahub-io/ai-gateway-operator/cmd/operator"
)

const operatorName = "opendatahub-ai-gateway-operator"

func main() {
	root := &cobra.Command{
		Use:          operatorName,
		SilenceUsage: true,
	}

	root.AddCommand(operator.NewCommand())
	root.AddCommand(chartgen.NewCommand())

	if err := root.ExecuteContext(ctrl.SetupSignalHandler()); err != nil {
		os.Exit(1)
	}
}
