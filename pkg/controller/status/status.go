package status

const (
	ConditionDeploymentsAvailable = "DeploymentsAvailable"

	// NoSubModuleManagedReason is set on DeploymentsAvailable when all sub-modules are Removed
	NoSubModuleManagedReason = "NoSubModuleManaged"

	// MaaSRemovalInProgressReason is set on DeploymentsAvailable while MaaS teardown is still running.
	MaaSRemovalInProgressReason = "MaaSRemovalInProgress"
)
