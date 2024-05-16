package capi2mapi

type MachineAndMachineTemplate interface {
	ToProviderSpec()
	ToMachine()
}

type MachineSetAndMachineTemplate interface {
	ToMachineSet()
}
