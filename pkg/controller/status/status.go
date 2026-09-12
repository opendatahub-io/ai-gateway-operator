package status

const (
	ConditionDeploymentsAvailable = "DeploymentsAvailable"

	// Per-sub-module ready conditions on the AIGateway CR.
	// These let the Dashboard (and any other consumer) observe each sub-module
	// independently — e.g. AIGateway enabled without MaaS gives
	// ModelsAsAServiceReady=False even though AIGateway itself is Ready.
	ConditionModelsAsAServiceReady = "ModelsAsAServiceReady"
	ConditionBatchGatewayReady     = "BatchGatewayReady"

	// NoSubModuleManagedReason is set on DeploymentsAvailable when all sub-modules are Removed
	NoSubModuleManagedReason = "NoSubModuleManaged"

	// MaaSRemovalInProgressReason is set on DeploymentsAvailable while MaaS teardown is still running.
	MaaSRemovalInProgressReason = "MaaSRemovalInProgress"

	// SubModuleRemovedReason is set on a sub-module condition when its managementState is Removed.
	SubModuleRemovedReason = "Removed"
	// SubModuleReadyReason is set on a sub-module condition when it is deployed and available.
	SubModuleReadyReason = "Ready"
	// SubModuleNotReadyReason is set when a sub-module is Managed but not yet available.
	SubModuleNotReadyReason = "NotReady"

	// Per-tenant health aggregation condition (ADR ODH-ADR-MS-0003 three-state model).
	// The maas-controller writes the source data to Config/default status; the
	// ai-gateway-operator mirrors it onto the AIGateway CR via this condition.
	ConditionTenantsHealthy = "TenantsHealthy"

	TenantsHealthyReason  = "AllTenantsHealthy"
	TenantsDegradedReason = "TenantsDegraded"
	TenantsBlockedReason  = "TenantsBlocked"
	TenantsNoneReason     = "NoTenantsFound"
)
