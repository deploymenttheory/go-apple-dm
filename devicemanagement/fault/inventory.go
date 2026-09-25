package fault

// Device inventory: collected attributes, the jobs that gather them and the schedules
// that drive those jobs.
var (
	InventoryNotFound = NewClient(
		"DM-INVENTORY-NOT-FOUND", NotFound,
		"the inventory record does not exist",
	)
	InventoryInvalid = NewClient(
		"DM-INVENTORY-INVALID", InvalidArgument,
		"the inventory request is not acceptable",
	)
	InventoryConflict = NewClient(
		"DM-INVENTORY-CONFLICT", Conflict,
		"the identity or revision conflicts with the stored record",
	)
	InventoryJobStopped = NewClient(
		"DM-INVENTORY-JOB-STOPPED", Conflict,
		"the inventory job has stopped",
	)
)
