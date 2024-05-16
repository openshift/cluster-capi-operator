package capi2mapi

// type MachineTemplateSpec interface {
// 	ToProviderSpec()
// }

type MachineAndMachineTemplate interface {
	ToProviderSpec()
	ToMachine()
}

type MachineSetAndMachineTemplate interface {
	ToMachineSet()
}
