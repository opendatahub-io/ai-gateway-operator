package support

import "os"

const (
	DefaultOperatorNamespace        = "opendatahub-ai-gateway-system"
	DefaultIntegrationTestNamespace = "integration-test"
)

func OperatorNamespace() string {
	if namespace := os.Getenv("OPERATOR_NAMESPACE"); namespace != "" {
		return namespace
	}

	return DefaultOperatorNamespace
}

func IntegrationTestNamespace() string {
	if namespace := os.Getenv("INTEGRATION_TEST_NAMESPACE"); namespace != "" {
		return namespace
	}

	return DefaultIntegrationTestNamespace
}

func HelmNamespace() string {
	return OperatorNamespace()
}
