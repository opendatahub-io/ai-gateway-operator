package support

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestOperatorNamespaceUsesEnvironmentOverride(t *testing.T) {
	g := NewWithT(t)
	t.Setenv("OPERATOR_NAMESPACE", "custom-namespace")

	g.Expect(OperatorNamespace()).To(Equal("custom-namespace"))
}

func TestOperatorNamespaceFallsBackToDefault(t *testing.T) {
	g := NewWithT(t)
	t.Setenv("OPERATOR_NAMESPACE", "")

	g.Expect(OperatorNamespace()).To(Equal(DefaultOperatorNamespace))
}

func TestIntegrationTestNamespaceUsesEnvironmentOverride(t *testing.T) {
	g := NewWithT(t)
	t.Setenv("INTEGRATION_TEST_NAMESPACE", "custom-integration")

	g.Expect(IntegrationTestNamespace()).To(Equal("custom-integration"))
}

func TestIntegrationTestNamespaceFallsBackToDefault(t *testing.T) {
	g := NewWithT(t)
	t.Setenv("INTEGRATION_TEST_NAMESPACE", "")

	g.Expect(IntegrationTestNamespace()).To(Equal(DefaultIntegrationTestNamespace))
}
