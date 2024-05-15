package capi2mapi

type MachineTemplateSpec interface {
	ToProviderSpec()
}

type MachineAndMachineTemplate interface {
	ToMachine()
}

type MachineSetAndMachineTemplate interface {
	ToMachineSet()
}
